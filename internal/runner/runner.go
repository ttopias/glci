package runner

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ttopias/glci/internal/gitlabci"
)

type Options struct {
	Root          string
	WorkDir       string
	Jobs          []gitlabci.Job
	Pipeline      *gitlabci.Pipeline
	Executor      string   // docker (default) | shell
	IncludeManual bool     // when:manual jobs run only with this or --job NAME
	SelectedJobs  []string // --job names; a matching manual job is treated as specified
	DryRun        bool
	Debug         bool // keep .glci/builds and .glci/tmp; also dump job.json + variables.env per job
	Privileged    bool
	Concurrency   int
	DefaultImage  string
	TriggerDepth  int
	Compile       gitlabci.CompileOptions
	Stdout        io.Writer
	Stderr        io.Writer
}

type Result struct {
	Name          string        `json:"name"`
	Status        string        `json:"status"`
	ExitCode      int           `json:"exit_code"`
	Duration      time.Duration `json:"duration"`
	Skipped       bool          `json:"skipped,omitempty"`
	AllowFail     bool          `json:"allow_failure,omitempty"`
	Coverage      string        `json:"coverage,omitempty"`
	LogPath       string        `json:"log"`
	ArtifactsPath string        `json:"artifacts,omitempty"`
}

func Run(opts Options) ([]Result, error) {
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 4
	}
	if opts.Executor == "" || opts.Executor == "auto" {
		opts.Executor = "docker"
	}
	if opts.DefaultImage == "" {
		opts.DefaultImage = "alpine:3.24"
	}
	if opts.WorkDir == "" {
		opts.WorkDir = filepath.Join(opts.Root, ".glci")
	}
	if err := prepareWorkDir(opts.WorkDir, opts.TriggerDepth == 0); err != nil {
		return nil, err
	}
	if !opts.Debug {
		defer cleanupTemp(opts.WorkDir)
	}

	jobs := opts.Jobs
	byName := map[string]gitlabci.Job{}
	for _, j := range jobs {
		byName[j.Name] = j
	}
	results := map[string]*Result{}
	var mu sync.Mutex
	failed := false
	dotenv := map[string]map[string]string{}

	ready := func(j gitlabci.Job) bool {
		deps := jobDeps(j, jobs, opts.Pipeline.Stages)
		for _, d := range deps {
			if _, ok := byName[d]; !ok {
				continue
			}
			r := results[d]
			if r == nil {
				return false
			}
		}
		return true
	}

	shouldRun := func(j gitlabci.Job) (bool, string) {
		if j.When == "never" {
			return false, "when:never"
		}
		if j.When == "manual" && !manualAllowed(j.Name, opts) {
			return false, "manual"
		}
		deps := jobDeps(j, jobs, opts.Pipeline.Stages)
		depFailed := false
		depRan := false
		for _, d := range deps {
			if _, ok := byName[d]; !ok {
				if j.When != "always" {
					return false, "needs missing"
				}
				continue
			}
			r := results[d]
			if r == nil {
				continue
			}
			if r.Skipped {
				// Explicit needs: a skipped needed job blocks (GitLab DAG).
				// Stage-ordered deps: skipped earlier-stage jobs do not poison
				// later jobs (only hard failures do). when:always ignores skips.
				if j.When != "always" && j.HasNeeds {
					return false, "upstream skipped"
				}
				continue
			}
			depRan = true
			if r.Status == "failed" && !r.AllowFail {
				depFailed = true
			}
		}
		switch j.When {
		case "on_failure":
			if !depFailed {
				return false, "no failure"
			}
		case "always":
			return true, ""
		default: // on_success, delayed, manual
			if depFailed && depRan {
				return false, "upstream failed"
			}
		}
		return true, ""
	}

	sem := make(chan struct{}, opts.Concurrency)
	var wg sync.WaitGroup
	groups := map[string]*sync.Mutex{}
	groupMu := sync.Mutex{}
	stageSeen := map[string]bool{}
	var stageMu sync.Mutex

	printStage := func(stage string) {
		stageMu.Lock()
		defer stageMu.Unlock()
		if stageSeen[stage] {
			return
		}
		stageSeen[stage] = true
		fmt.Fprintf(opts.Stdout, "\n── stage: %s ──\n", stage)
	}

	for {
		mu.Lock()
		var scheduled []gitlabci.Job
		allDone := true
		for _, j := range jobs {
			if results[j.Name] != nil {
				continue
			}
			allDone = false
			if ready(j) {
				scheduled = append(scheduled, j)
			}
		}
		// Claim slots so we don't reschedule the same job.
		for _, j := range scheduled {
			results[j.Name] = &Result{Name: j.Name, Status: "running"}
		}
		mu.Unlock()
		if allDone && len(scheduled) == 0 {
			break
		}
		if len(scheduled) == 0 {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		for _, j := range scheduled {
			j := j
			wg.Add(1)
			go func() {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				if j.ResourceGroup != "" {
					groupMu.Lock()
					m := groups[j.ResourceGroup]
					if m == nil {
						m = &sync.Mutex{}
						groups[j.ResourceGroup] = m
					}
					groupMu.Unlock()
					m.Lock()
					defer m.Unlock()
				}

				ok, reason := shouldRun(j)
				if !ok {
					res := Result{Name: j.Name, Status: "skipped", Skipped: true}
					res.LogPath = writeSkipLog(opts, j.Name, reason)
					mu.Lock()
					results[j.Name] = &res
					mu.Unlock()
					printStage(j.Stage)
					fmt.Fprintf(opts.Stdout, "○ skip  %s (%s)\n", j.Name, reason)
					return
				}
				if j.When == "delayed" && j.StartIn > 0 && !opts.DryRun {
					printStage(j.Stage)
					fmt.Fprintf(opts.Stdout, "… delay %s %s\n", j.Name, j.StartIn)
					time.Sleep(j.StartIn)
				}

				mu.Lock()
				mergedDot := map[string]string{}
				for _, d := range artifactSources(j, jobs, opts.Pipeline.Stages) {
					for k, v := range dotenv[d] {
						mergedDot[k] = v
					}
				}
				pipelineFailed := failed
				mu.Unlock()
				_ = pipelineFailed

				for k, v := range mergedDot {
					j.Variables[k] = v
				}

				printStage(j.Stage)
				fmt.Fprintf(opts.Stdout, "▶ start %s\n", j.Name)
				start := time.Now()
				res := executeJob(opts, j)
				res.Duration = time.Since(start)
				res.AllowFail = j.AllowFailure || containsInt(j.AllowFailureCodes, res.ExitCode)
				if res.Status == "failed" && res.AllowFail {
					res.Status = "failed-allowed"
				}
				printJobResult(opts, j.Name, res)

				if artifactsWhen(j, res) {
					if dj, err := collectDotenv(opts, j); err == nil && len(dj) > 0 {
						mu.Lock()
						dotenv[j.Name] = dj
						mu.Unlock()
					}
				}

				mu.Lock()
				results[j.Name] = &res
				if res.Status == "failed" && !res.AllowFail {
					failed = true
				}
				mu.Unlock()
			}()
		}
		wg.Wait()
	}

	var out []Result
	for _, j := range jobs {
		if r := results[j.Name]; r != nil {
			out = append(out, *r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	writeReport(opts, out)
	printJobOutputs(opts, out)

	pipeFail := false
	for _, r := range out {
		if r.Status == "failed" && !r.AllowFail {
			pipeFail = true
		}
	}
	if pipeFail {
		return out, fmt.Errorf("pipeline failed")
	}
	return out, nil
}

func jobSelected(name string, names []string) bool {
	for _, n := range names {
		if gitlabci.MatchJobName(name, n) {
			return true
		}
	}
	return false
}

// manualAllowed decides whether a when:manual job may run.
// --job NAME opts that job in. With SelectedJobs, IncludeManual must not unlock
// stage-sibling manuals FilterJobs pulled in; manuals on a selected job's
// explicit needs path still run as real dependencies.
func manualAllowed(name string, opts Options) bool {
	if jobSelected(name, opts.SelectedJobs) {
		return true
	}
	if len(opts.SelectedJobs) == 0 {
		return opts.IncludeManual
	}
	return inNeedsClosure(name, opts.SelectedJobs, opts.Jobs)
}

// inNeedsClosure reports whether name is reachable from any selected job by
// walking only explicit needs edges (never stage ordering).
func inNeedsClosure(name string, selected []string, jobs []gitlabci.Job) bool {
	byName := map[string]gitlabci.Job{}
	for _, j := range jobs {
		byName[j.Name] = j
	}
	seen := map[string]bool{}
	var stack []string
	for _, s := range selected {
		for _, j := range jobs {
			if gitlabci.MatchJobName(j.Name, s) {
				stack = append(stack, j.Name)
			}
		}
	}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[n] {
			continue
		}
		seen[n] = true
		j, ok := byName[n]
		if !ok || !j.HasNeeds {
			continue
		}
		for _, dep := range matchedNeedJobs(j, jobs) {
			if dep == name {
				return true
			}
			stack = append(stack, dep)
		}
	}
	return false
}

// matchedNeedJobs resolves a job's explicit needs to concrete job names
// (matrix/parallel aware). Unlike jobDeps, unmatched needs are omitted.
func matchedNeedJobs(j gitlabci.Job, all []gitlabci.Job) []string {
	var d []string
	for _, n := range j.Needs {
		if n.Job == "" {
			continue
		}
		for _, other := range all {
			if !gitlabci.MatchJobName(other.Name, n.Job) {
				continue
			}
			if n.Parallel != nil && !matrixMatch(other.Matrix, n.Parallel) {
				continue
			}
			d = append(d, other.Name)
		}
	}
	return d
}

func expandJobNames(spec string, all []gitlabci.Job) []string {
	var out []string
	for _, other := range all {
		if gitlabci.MatchJobName(other.Name, spec) {
			out = append(out, other.Name)
		}
	}
	if len(out) == 0 {
		return []string{spec}
	}
	return out
}

func jobDeps(j gitlabci.Job, all []gitlabci.Job, stages []string) []string {
	if j.HasNeeds {
		var d []string
		for _, n := range j.Needs {
			if n.Job == "" {
				continue
			}
			matched := false
			for _, other := range all {
				if gitlabci.MatchJobName(other.Name, n.Job) {
					if n.Parallel != nil && !matrixMatch(other.Matrix, n.Parallel) {
						continue
					}
					d = append(d, other.Name)
					matched = true
				}
			}
			if !matched && !n.Optional {
				d = append(d, n.Job)
			}
		}
		return d
	}
	si := stageIndex(stages, j.Stage)
	var d []string
	for _, other := range all {
		if stageIndex(stages, other.Stage) < si {
			d = append(d, other.Name)
		}
	}
	return d
}

// artifactSources returns job names whose artifacts should be restored into j.
// Mirrors GitLab Rails Ci::BuildDependencies:
//   - dependencies: [] (non-nil empty) → restore nothing, even if needs is set
//   - else candidates = needs with artifacts:true (if HasNeeds) OR previous stages
//   - if dependencies: [a, b] non-empty → intersect candidates with that list
func artifactSources(j gitlabci.Job, all []gitlabci.Job, stages []string) []string {
	if j.Dependencies != nil && len(j.Dependencies) == 0 {
		return []string{}
	}

	var candidates []string
	if j.HasNeeds {
		for _, n := range j.Needs {
			if !n.Artifacts || n.Job == "" {
				continue
			}
			matched := false
			for _, other := range all {
				if !gitlabci.MatchJobName(other.Name, n.Job) {
					continue
				}
				if n.Parallel != nil && !matrixMatch(other.Matrix, n.Parallel) {
					continue
				}
				candidates = append(candidates, other.Name)
				matched = true
			}
			if !matched {
				candidates = append(candidates, n.Job)
			}
		}
	} else {
		si := stageIndex(stages, j.Stage)
		for _, other := range all {
			if stageIndex(stages, other.Stage) < si {
				candidates = append(candidates, other.Name)
			}
		}
	}

	if j.Dependencies == nil {
		return candidates
	}
	allowed := map[string]bool{}
	for _, name := range j.Dependencies {
		for _, exp := range expandJobNames(name, all) {
			allowed[exp] = true
		}
	}
	out := make([]string, 0, len(candidates))
	for _, c := range candidates {
		if allowed[c] {
			out = append(out, c)
		}
	}
	return out
}

func matrixMatch(have, want map[string]string) bool {
	for k, v := range want {
		if have[k] != v {
			return false
		}
	}
	return true
}

func stageIndex(stages []string, s string) int {
	for i, x := range stages {
		if x == s {
			return i
		}
	}
	return 0
}

func artifactsWhen(j gitlabci.Job, r Result) bool {
	if r.Skipped {
		return false
	}
	when := "on_success"
	if j.Artifacts != nil && j.Artifacts.When != "" {
		when = j.Artifacts.When
	}
	switch when {
	case "always":
		return true
	case "on_failure":
		return r.Status == "failed" || r.Status == "failed-allowed"
	default:
		return r.Status == "success"
	}
}

func containsInt(ns []int, n int) bool {
	for _, x := range ns {
		if x == n {
			return true
		}
	}
	return false
}

func executeJob(opts Options, j gitlabci.Job) Result {
	if opts.DryRun {
		return Result{Name: j.Name, Status: "success", LogPath: writeSkipLog(opts, j.Name, "dry-run")}
	}
	if j.Trigger != nil {
		res := runTrigger(opts, j)
		if res.LogPath == "" {
			res.LogPath = writeSkipLog(opts, j.Name, "child pipeline "+res.Status)
		}
		return res
	}

	attempts := 1
	if j.Retry != nil {
		attempts = j.Retry.Max + 1
	}
	var last Result
	for try := 0; try < attempts; try++ {
		last = runOnce(opts, j)
		if last.Status == "success" {
			return last
		}
		if j.Retry == nil || try == attempts-1 {
			return last
		}
		if !shouldRetry(j.Retry, last) {
			return last
		}
		fmt.Fprintf(opts.Stdout, "↻ retry %s (%d/%d)\n", j.Name, try+1, j.Retry.Max)
	}
	return last
}

func shouldRetry(r *gitlabci.Retry, last Result) bool {
	if len(r.ExitCodes) > 0 {
		return containsInt(r.ExitCodes, last.ExitCode)
	}
	for _, w := range r.When {
		if w == "always" || w == "script_failure" {
			return true
		}
	}
	return false
}

func runOnce(opts Options, j gitlabci.Job) Result {
	j.Variables = gitlabci.ExpandMap(mergeCopy(j.Variables))
	build := filepath.Join(opts.WorkDir, "builds", safe(j.Name))
	_ = os.RemoveAll(build)
	if err := copyTree(opts.Root, build, map[string]bool{".glci": true}); err != nil {
		return Result{Name: j.Name, Status: "failed", ExitCode: 1}
	}
	restoreArtifacts(opts, j, build)
	restoreCaches(opts, j, build)
	writeFileVariables(j, build)

	j.Variables["CI_PROJECT_DIR"] = build
	j.Variables["CI_JOB_ID"] = gitlabci.NewID()
	j.Variables["CI_JOB_NAME"] = j.Name
	j.Variables["CI_JOB_STAGE"] = j.Stage
	injectSecrets(j)

	if opts.Debug {
		writeJobDebug(build, j)
	}

	scriptPath := filepath.Join(opts.WorkDir, "tmp", safe(j.Name)+".sh")
	if err := os.WriteFile(scriptPath, []byte(renderScript(j)), 0o755); err != nil {
		return Result{Name: j.Name, Status: "failed", ExitCode: 1}
	}

	logPath := filepath.Join(opts.WorkDir, "logs", safe(j.Name)+".log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return Result{Name: j.Name, Status: "failed", ExitCode: 1}
	}
	jobOpts := opts
	jobOpts.Stdout = io.MultiWriter(opts.Stdout, logFile)
	jobOpts.Stderr = io.MultiWriter(opts.Stderr, logFile)

	execKind := opts.Executor
	if execKind == "" || execKind == "auto" {
		execKind = "docker"
	}

	var (
		code int
		log  string
	)
	switch execKind {
	case "shell":
		code, log, err = runShell(jobOpts, j, build, scriptPath)
	default:
		if j.Image == nil || j.Image.Name == "" {
			j.Image = &gitlabci.Image{Name: opts.DefaultImage}
		}
		code, log, err = runDocker(jobOpts, j, build, scriptPath)
		reclaimWorkspace(build)
	}
	_ = logFile.Close()

	res := Result{Name: j.Name, ExitCode: code, Status: "success", LogPath: logPath}
	if err != nil || code != 0 {
		res.Status = "failed"
		if err != nil && code == 0 {
			res.ExitCode = 1
		}
		if err != nil {
			fmt.Fprintf(opts.Stderr, "job %s: %v\n", j.Name, err)
		}
	}
	if j.Coverage != "" {
		res.Coverage = extractCoverage(log, j.Coverage)
		if res.Coverage != "" {
			fmt.Fprintf(opts.Stdout, "    coverage: %s\n", res.Coverage)
		}
	}

	if artifactsWhen(j, res) {
		saveArtifacts(opts, j, build)
	}
	saveCaches(opts, j, build, res)
	if j.Pages != nil {
		savePages(opts, build)
	}
	artDir := filepath.Join(opts.WorkDir, "artifacts", safe(j.Name))
	if st, err := os.Stat(artDir); err == nil && st.IsDir() {
		res.ArtifactsPath = artDir
	}
	if j.Release != nil && len(j.Release) > 0 {
		_ = os.WriteFile(filepath.Join(opts.WorkDir, "release-"+safe(j.Name)+".json"), mustJSON(j.Release), 0o644)
	}
	if j.Environment != nil {
		recordEnv(opts, j, res)
	}
	return res
}

func injectSecrets(j gitlabci.Job) {
	for name, raw := range j.Secrets {
		m, _ := raw.(map[string]any)
		if m == nil {
			continue
		}
		env, _ := m["file"].(bool)
		val := os.Getenv("GLCI_SECRET_" + name)
		if val == "" {
			val = "local-secret-" + name
		}
		if env {
			j.Variables[name] = val
		} else {
			j.Variables[name] = val
		}
	}
	if len(j.IDTokens) > 0 {
		j.Variables["GLCI_ID_TOKEN"] = "local-id-token"
		for name := range j.IDTokens {
			j.Variables[name] = "eyJhbGciOiJub25lIn0.eyJhdWQiOiJsb2NhbCIsImlzcyI6ImdsY2kifQ."
		}
	}
}

func renderScript(j gitlabci.Job) string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\nset -e\n")
	b.WriteString("if [ -n \"$CI_PROJECT_DIR\" ]; then cd \"$CI_PROJECT_DIR\"; fi\n")
	writeSection := func(title string, lines []string) {
		if len(lines) == 0 {
			return
		}
		fmt.Fprintf(&b, "echo '$ %s'\n", title)
		for _, line := range lines {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	writeSection("hooks:pre_get_sources_script", j.PreGetSources)
	writeSection("before_script", j.BeforeScript)
	b.WriteString("set +e\n")
	b.WriteString("glci_status=0\n")
	b.WriteString("(\nset -e\n")
	for _, line := range j.Script {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteString(")\nglci_status=$?\nset -e\n")
	writeSection("after_script", j.AfterScript)
	b.WriteString("exit $glci_status\n")
	return b.String()
}

func envList(vars map[string]string) []string {
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+vars[k])
	}
	return out
}

func safe(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == ' ' || r == ':' || r == '[' || r == ']' {
			return '_'
		}
		return r
	}, s)
	if len(s) > 80 {
		s = s[:80]
	}
	return s
}

func mustJSON(v any) []byte {
	b, _ := json.MarshalIndent(v, "", "  ")
	return b
}

func recordEnv(opts Options, j gitlabci.Job, res Result) {
	p := filepath.Join(opts.WorkDir, "environments.json")
	data := []map[string]any{}
	if b, err := os.ReadFile(p); err == nil {
		_ = json.Unmarshal(b, &data)
	}
	data = append(data, map[string]any{
		"name": j.Environment.Name, "url": j.Environment.URL, "job": j.Name,
		"status": res.Status, "action": j.Environment.Action,
	})
	_ = os.WriteFile(p, mustJSON(data), 0o644)
}

func copyTree(src, dst string, skip map[string]bool) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(dst, 0o755)
		}
		base := strings.Split(rel, string(os.PathSeparator))[0]
		if skip[base] {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		}
		return copyFile(path, target, info.Mode())
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func copyGlob(srcDir, pattern, dstDir string, exclude []string) error {
	if filepath.IsAbs(pattern) || strings.Contains(pattern, "..") {
		return fmt.Errorf("artifact path %q escapes the job workspace", pattern)
	}
	matches, err := filepath.Glob(filepath.Join(srcDir, filepath.FromSlash(pattern)))
	if err != nil {
		return err
	}
	// Also walk for ** style
	if strings.Contains(pattern, "**") || len(matches) == 0 {
		_ = filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return err
			}
			if !insideDir(srcDir, path) {
				return nil
			}
			rel, _ := filepath.Rel(srcDir, path)
			if matchPath(pattern, filepath.ToSlash(rel)) {
				matches = append(matches, path)
			}
			return nil
		})
	}
	for _, m := range matches {
		if !insideDir(srcDir, m) {
			continue
		}
		rel, err := filepath.Rel(srcDir, m)
		if err != nil {
			continue
		}
		if excluded(filepath.ToSlash(rel), exclude) {
			continue
		}
		info, err := os.Stat(m)
		if err != nil {
			continue
		}
		dst := filepath.Join(dstDir, rel)
		if !insideDir(dstDir, dst) {
			continue
		}
		if info.IsDir() {
			_ = copyTree(m, dst, nil)
			continue
		}
		_ = copyFile(m, dst, info.Mode())
	}
	return nil
}

func insideDir(root, p string) bool {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	absP, err := filepath.Abs(p)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absRoot, absP)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func matchPath(pattern, name string) bool {
	pattern = strings.TrimPrefix(pattern, "./")
	name = strings.TrimPrefix(name, "./")
	ok, _ := filepath.Match(pattern, name)
	if ok {
		return true
	}
	if strings.HasSuffix(pattern, "/") {
		return strings.HasPrefix(name, pattern)
	}
	if strings.Contains(pattern, "**") {
		parts := strings.SplitN(pattern, "**", 2)
		pre := strings.TrimSuffix(parts[0], "/")
		suf := strings.TrimPrefix(parts[1], "/")
		if pre != "" && !strings.HasPrefix(name, pre) {
			return false
		}
		if suf == "" {
			return true
		}
		ok, _ = filepath.Match(suf, filepath.Base(name))
		return ok || strings.HasSuffix(name, strings.TrimPrefix(suf, "*/"))
	}
	ok, _ = filepath.Match(pattern, filepath.Base(name))
	return ok
}

func restoreArtifacts(opts Options, j gitlabci.Job, build string) {
	for _, name := range artifactSources(j, opts.Jobs, opts.Pipeline.Stages) {
		dir := filepath.Join(opts.WorkDir, "artifacts", safe(name))
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			_ = copyTree(dir, build, nil)
		}
	}
}

func saveArtifacts(opts Options, j gitlabci.Job, build string) {
	if j.Artifacts == nil && j.Pages == nil {
		return
	}
	dir := filepath.Join(opts.WorkDir, "artifacts", safe(j.Name))
	_ = os.RemoveAll(dir)
	_ = os.MkdirAll(dir, 0o755)
	paths := []string{}
	exclude := []string{}
	if j.Artifacts != nil {
		paths = j.Artifacts.Paths
		exclude = j.Artifacts.Exclude
		if j.Artifacts.Untracked {
			paths = append(paths, untrackedFiles(build)...)
		}
		paths = append(paths, j.Artifacts.Dotenv...)
	}
	if j.Pages != nil && len(paths) == 0 {
		if j.Publish != "" {
			paths = []string{j.Publish}
		} else {
			paths = []string{"public"}
		}
	}
	for _, p := range paths {
		_ = copyGlob(build, p, dir, exclude)
	}
}

func restoreCaches(opts Options, j gitlabci.Job, build string) {
	for _, c := range j.Cache {
		if c.Policy == "push" {
			continue
		}
		keys := append([]string{cacheKey(c, build)}, c.Fallback...)
		for _, k := range keys {
			dir := filepath.Join(opts.WorkDir, "cache", safe(k))
			if st, err := os.Stat(dir); err == nil && st.IsDir() {
				_ = copyTree(dir, build, nil)
				break
			}
		}
	}
}

func saveCaches(opts Options, j gitlabci.Job, build string, res Result) {
	for _, c := range j.Cache {
		if c.Policy == "pull" {
			continue
		}
		ok := res.Status == "success"
		if c.When == "always" {
			ok = true
		}
		if c.When == "on_failure" {
			ok = res.Status == "failed"
		}
		if !ok {
			continue
		}
		dir := filepath.Join(opts.WorkDir, "cache", safe(cacheKey(c, build)))
		_ = os.MkdirAll(dir, 0o755)
		for _, p := range c.Paths {
			_ = copyGlob(build, p, dir, nil)
		}
	}
}

func savePages(opts Options, build string) {
	src := filepath.Join(build, "public")
	dst := filepath.Join(opts.WorkDir, "pages")
	if st, err := os.Stat(src); err == nil && st.IsDir() {
		_ = os.RemoveAll(dst)
		_ = copyTree(src, dst, nil)
	}
}

func collectDotenv(opts Options, j gitlabci.Job) (map[string]string, error) {
	out := map[string]string{}
	if j.Artifacts == nil {
		return out, nil
	}
	dir := filepath.Join(opts.WorkDir, "artifacts", safe(j.Name))
	for _, f := range j.Artifacts.Dotenv {
		b, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			b, err = os.ReadFile(filepath.Join(opts.WorkDir, "builds", safe(j.Name), f))
			if err != nil {
				continue
			}
		}
		for _, line := range bytes.Split(b, []byte("\n")) {
			s := strings.TrimSpace(string(line))
			if s == "" || strings.HasPrefix(s, "#") {
				continue
			}
			k, v, ok := strings.Cut(s, "=")
			if ok {
				out[k] = strings.Trim(v, `"'`)
			}
		}
	}
	return out, nil
}

func dockerAvailable() bool {
	return dockerPing() == nil
}

func prepareWorkDir(dir string, reset bool) error {
	if reset {
		if err := resetWorkDir(dir); err != nil {
			return err
		}
	}
	for _, d := range []string{"builds", "artifacts", "cache", "pages", "logs", "tmp"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			return err
		}
	}
	return nil
}

func resetWorkDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if e.Name() == "cache" {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

func cleanupTemp(dir string) {
	for _, name := range []string{"tmp", "builds"} {
		_ = os.RemoveAll(filepath.Join(dir, name))
	}
}

func writeSkipLog(opts Options, name, reason string) string {
	if opts.WorkDir == "" {
		return ""
	}
	p := filepath.Join(opts.WorkDir, "logs", safe(name)+".log")
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	_ = os.WriteFile(p, []byte("skipped: "+reason+"\n"), 0o644)
	return p
}

func printJobResult(opts Options, name string, res Result) {
	dur := res.Duration.Truncate(time.Millisecond)
	switch res.Status {
	case "success":
		fmt.Fprintf(opts.Stdout, "✓ ok    %s in %s\n", name, dur)
	case "failed-allowed":
		fmt.Fprintf(opts.Stdout, "⚠ fail  %s in %s (allowed)\n", name, dur)
		if res.LogPath != "" {
			fmt.Fprintf(opts.Stdout, "  log: %s\n", res.LogPath)
		}
	case "failed":
		fmt.Fprintf(opts.Stdout, "✗ fail  %s in %s\n", name, dur)
		if res.LogPath != "" {
			fmt.Fprintf(opts.Stdout, "  log: %s\n", res.LogPath)
		}
	default:
		fmt.Fprintf(opts.Stdout, "• %-5s %s in %s\n", res.Status, name, dur)
	}
}

// writeJobDebug dumps the compiled job and effective variables under the job
// build tree. Values are written as-is (including secrets / dummy JWTs); there
// is no extra redaction. Files are mode 0600.
func writeJobDebug(build string, j gitlabci.Job) {
	dir := filepath.Join(build, ".glci")
	_ = os.MkdirAll(dir, 0o755)
	if b, err := json.MarshalIndent(j, "", "  "); err == nil {
		_ = os.WriteFile(filepath.Join(dir, "job.json"), b, 0o600)
	}
	keys := make([]string, 0, len(j.Variables))
	for k := range j.Variables {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("# Effective job variables (no redaction). Written by glci --debug.\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "%s=%s\n", k, j.Variables[k])
	}
	_ = os.WriteFile(filepath.Join(dir, "variables.env"), []byte(b.String()), 0o600)
}

func writeReport(opts Options, results []Result) {
	if opts.WorkDir == "" {
		return
	}
	type row struct {
		Name      string `json:"name"`
		Status    string `json:"status"`
		ExitCode  int    `json:"exit_code"`
		Duration  string `json:"duration"`
		Log       string `json:"log"`
		Artifacts string `json:"artifacts,omitempty"`
		Coverage  string `json:"coverage,omitempty"`
	}
	rows := make([]row, 0, len(results))
	for _, r := range results {
		item := row{
			Name: r.Name, Status: r.Status, ExitCode: r.ExitCode,
			Duration: r.Duration.Truncate(time.Millisecond).String(),
			Log:      r.LogPath, Artifacts: r.ArtifactsPath, Coverage: r.Coverage,
		}
		rows = append(rows, item)
	}
	_ = os.WriteFile(filepath.Join(opts.WorkDir, "report.json"), mustJSON(map[string]any{
		"jobs": rows,
	}), 0o644)
}

func printJobOutputs(opts Options, results []Result) {
	fmt.Fprintf(opts.Stdout, "\n── summary ──\n")
	failed := 0
	for _, r := range results {
		fmt.Fprintf(opts.Stdout, "  %-28s %-16s", r.Name, r.Status)
		if r.LogPath != "" {
			fmt.Fprintf(opts.Stdout, "  log=%s", r.LogPath)
		}
		if r.ArtifactsPath != "" {
			fmt.Fprintf(opts.Stdout, "  artifacts=%s", r.ArtifactsPath)
		}
		fmt.Fprintln(opts.Stdout)
		if r.Status == "failed" && !r.AllowFail {
			failed++
		}
	}
	fmt.Fprintf(opts.Stdout, "  report=%s\n", filepath.Join(opts.WorkDir, "report.json"))
	if failed > 0 {
		fmt.Fprintf(opts.Stdout, "\n%d job(s) failed — see log paths above\n", failed)
	}
}

func mergeCopy(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func writeFileVariables(j gitlabci.Job, build string) {
	if len(j.FileVariables) == 0 {
		return
	}
	dir := filepath.Join(build, ".glci-file-vars")
	_ = os.MkdirAll(dir, 0o700)
	for name, content := range j.FileVariables {
		p := filepath.Join(dir, name)
		_ = os.WriteFile(p, []byte(content), 0o400)
		j.Variables[name] = p
	}
}

func remapFileVariables(j gitlabci.Job, projectDir string) {
	for name := range j.FileVariables {
		j.Variables[name] = filepath.ToSlash(filepath.Join(projectDir, ".glci-file-vars", name))
	}
}

func extractCoverage(log, pattern string) string {
	pattern = strings.TrimSpace(pattern)
	pattern = strings.TrimPrefix(pattern, "/")
	if i := strings.LastIndex(pattern, "/"); i > 0 {
		pattern = pattern[:i]
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return ""
	}
	matches := re.FindAllStringSubmatch(log, -1)
	if len(matches) == 0 {
		return ""
	}
	m := matches[len(matches)-1]
	if len(m) > 1 {
		return m[1]
	}
	return m[0]
}

func cacheKey(c gitlabci.Cache, build string) string {
	if len(c.KeyFiles) == 0 {
		if c.Key == "" {
			return "default"
		}
		return c.Key
	}
	h := sha256.New()
	for _, f := range c.KeyFiles {
		b, _ := os.ReadFile(filepath.Join(build, f))
		h.Write(b)
		h.Write([]byte{0})
	}
	sum := hex.EncodeToString(h.Sum(nil))[:12]
	if c.KeyPrefix != "" {
		return c.KeyPrefix + "-" + sum
	}
	return sum
}

func excluded(rel string, patterns []string) bool {
	for _, p := range patterns {
		if matchPath(p, rel) {
			return true
		}
	}
	return false
}

func untrackedFiles(dir string) []string {
	cmd := exec.Command("git", "ls-files", "--others", "--exclude-standard")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var files []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			files = append(files, line)
		}
	}
	return files
}

func runTrigger(opts Options, j gitlabci.Job) Result {
	if opts.TriggerDepth > 10 {
		return Result{Name: j.Name, Status: "failed", ExitCode: 1}
	}
	file := ""
	root := opts.Root
	if len(j.Trigger.LocalFiles) > 0 {
		file = j.Trigger.LocalFiles[0]
	} else if j.Trigger.Project != "" {
		mapped := opts.Compile.Projects[j.Trigger.Project]
		if mapped == "" {
			fmt.Fprintf(opts.Stdout, "▶ trigger %s: project %q not mapped in .glci.yml\n", j.Name, j.Trigger.Project)
			return Result{Name: j.Name, Status: "success"}
		}
		if filepath.IsAbs(mapped) {
			root = mapped
		} else {
			root = filepath.Join(opts.Root, mapped)
		}
		file = ".gitlab-ci.yml"
	}
	if file == "" {
		fmt.Fprintf(opts.Stdout, "▶ trigger %s: no local child pipeline to run\n", j.Name)
		return Result{Name: j.Name, Status: "success"}
	}
	fmt.Fprintf(opts.Stdout, "▶ child  %s from %s\n", j.Name, file)
	copt := opts.Compile
	copt.Root = root
	copt.File = file
	copt.Source = "parent_pipeline"
	copt.Inputs = mergeCopy(j.Trigger.Inputs)
	extra := mergeCopy(j.Variables)
	delete(extra, "CI_PIPELINE_SOURCE")
	copt.ExtraVars = extra
	child, err := gitlabci.Compile(copt)
	if err != nil {
		fmt.Fprintf(opts.Stderr, "child pipeline: %v\n", err)
		if j.Trigger.Strategy == "depend" {
			return Result{Name: j.Name, Status: "failed", ExitCode: 1}
		}
		return Result{Name: j.Name, Status: "success"}
	}
	childOpts := opts
	childOpts.Root = root
	childOpts.Pipeline = child
	childOpts.Jobs = child.Jobs
	childOpts.TriggerDepth = opts.TriggerDepth + 1
	childOpts.WorkDir = filepath.Join(opts.WorkDir, "child-"+safe(j.Name))
	_, err = Run(childOpts)
	if err != nil && j.Trigger.Strategy == "depend" {
		return Result{Name: j.Name, Status: "failed", ExitCode: 1}
	}
	return Result{Name: j.Name, Status: "success"}
}
