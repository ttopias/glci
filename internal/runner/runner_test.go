package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ttopias/glci/internal/gitctx"
	"github.com/ttopias/glci/internal/gitlabci"
)

func TestRunShellPipeline(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".gitlab-ci.yml", `
stages: [build, test]
build:
  stage: build
  script:
    - echo hello > out.txt
    - echo FOO=bar > build.env
  artifacts:
    paths: [out.txt]
    reports:
      dotenv: build.env
test:
  stage: test
  script:
    - test -f out.txt
    - echo $FOO | grep bar
`)
	p, err := gitlabci.Compile(gitlabci.CompileOptions{
		Root: dir, File: ".gitlab-ci.yml",
		Git:    gitctx.Info{Root: dir, Branch: "main", Ref: "main", DefaultBranch: "main", SHA: "deadbeef", ShortSHA: "deadbeef"},
		Source: "push",
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := Run(Options{
		Root: dir, Pipeline: p, Jobs: p.Jobs, Executor: "shell", Concurrency: 2,
		Stdout: os.Stdout, Stderr: os.Stderr,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 {
		t.Fatalf("results=%d", len(res))
	}
	for _, r := range res {
		if r.Status != "success" {
			t.Fatalf("%s: %s", r.Name, r.Status)
		}
		if r.LogPath == "" {
			t.Fatalf("%s missing log path", r.Name)
		}
	}
	work := filepath.Join(dir, ".glci")
	if _, err := os.Stat(filepath.Join(work, "logs", "build.log")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(work, "artifacts", "build", "out.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(work, "report.json")); err != nil {
		t.Fatal(err)
	}
}

func TestKeepsFailedArtifactsAndLogs(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".gitlab-ci.yml", `
failing:
  stage: build
  script:
    - echo leftover > keep.txt
    - exit 1
  artifacts:
    when: always
    paths: [keep.txt]
manual_job:
  stage: test
  when: manual
  script: echo manual-ok
  artifacts:
    paths: [keep.txt]
  needs: []
`)
	p, err := gitlabci.Compile(gitlabci.CompileOptions{
		Root: dir, File: ".gitlab-ci.yml",
		Git:    gitctx.Info{Root: dir, Branch: "main", Ref: "main", DefaultBranch: "main"},
		Source: "push",
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := Run(Options{Root: dir, Pipeline: p, Jobs: p.Jobs, Executor: "shell", Stdout: os.Stdout, Stderr: os.Stderr})
	if err == nil {
		t.Fatal("expected pipeline failure")
	}
	by := map[string]Result{}
	for _, r := range res {
		by[r.Name] = r
	}
	if by["failing"].Status != "failed" {
		t.Fatalf("failing=%s", by["failing"].Status)
	}
	if !by["manual_job"].Skipped {
		t.Fatalf("manual should skip by default, got %+v", by["manual_job"])
	}
	if _, err := os.Stat(filepath.Join(dir, ".glci", "artifacts", "failing", "keep.txt")); err != nil {
		t.Fatalf("failed job artifacts missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".glci", "logs", "failing.log")); err != nil {
		t.Fatal(err)
	}
}

func TestNeedsSkipFailed(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".gitlab-ci.yml", `
fail:
  stage: build
  script: exit 1
ok:
  stage: test
  needs: [fail]
  script: echo should-skip
`)
	p, err := gitlabci.Compile(gitlabci.CompileOptions{
		Root: dir, File: ".gitlab-ci.yml",
		Git:    gitctx.Info{Root: dir, Branch: "main", Ref: "main", DefaultBranch: "main"},
		Source: "push",
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := Run(Options{Root: dir, Pipeline: p, Jobs: p.Jobs, Executor: "shell", Stdout: os.Stdout, Stderr: os.Stderr})
	if err == nil {
		t.Fatal("expected pipeline failure")
	}
	by := map[string]Result{}
	for _, r := range res {
		by[r.Name] = r
	}
	if by["fail"].Status != "failed" {
		t.Fatalf("fail=%s", by["fail"].Status)
	}
	if !by["ok"].Skipped {
		t.Fatalf("ok should skip, got %s", by["ok"].Status)
	}
	if _, err := os.Stat(filepath.Join(dir, ".glci", "logs", "ok.log")); err != nil {
		t.Fatal(err)
	}
}

func TestIncludeManualFlag(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".gitlab-ci.yml", `
auto:
  script: echo auto
manual_job:
  when: manual
  script: echo yes
  needs: []
`)
	p, err := gitlabci.Compile(gitlabci.CompileOptions{
		Root: dir, File: ".gitlab-ci.yml",
		Git:    gitctx.Info{Root: dir, Branch: "main", Ref: "main", DefaultBranch: "main"},
		Source: "push",
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := Run(Options{Root: dir, Pipeline: p, Jobs: p.Jobs, Executor: "shell", IncludeManual: true, Stdout: os.Stdout, Stderr: os.Stderr})
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]Result{}
	for _, r := range res {
		by[r.Name] = r
	}
	if by["auto"].Status != "success" {
		t.Fatalf("auto=%s", by["auto"].Status)
	}
	if by["manual_job"].Status != "success" || by["manual_job"].Skipped {
		t.Fatalf("manual should run with IncludeManual, got %+v", by["manual_job"])
	}
}

func TestSelectedJobRunsManual(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".gitlab-ci.yml", `
auto:
  script: echo auto
manual_job:
  when: manual
  script: echo yes
  needs: []
`)
	p, err := gitlabci.Compile(gitlabci.CompileOptions{
		Root: dir, File: ".gitlab-ci.yml",
		Git:    gitctx.Info{Root: dir, Branch: "main", Ref: "main", DefaultBranch: "main"},
		Source: "push",
	})
	if err != nil {
		t.Fatal(err)
	}
	jobs := gitlabci.FilterJobs(p, []string{"manual_job"})
	res, err := Run(Options{Root: dir, Pipeline: p, Jobs: jobs, Executor: "shell", SelectedJobs: []string{"manual_job"}, Stdout: os.Stdout, Stderr: os.Stderr})
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]Result{}
	for _, r := range res {
		by[r.Name] = r
	}
	if by["manual_job"].Status != "success" || by["manual_job"].Skipped {
		t.Fatalf("named manual should run, got %+v", by["manual_job"])
	}
}

func TestWorkDirRefreshAndTempCleanup(t *testing.T) {
	dir := t.TempDir()
	work := filepath.Join(dir, ".glci")
	write(t, dir, ".gitlab-ci.yml", `
old:
  script: echo first > a.txt
  artifacts:
    paths: [a.txt]
`)
	write(t, work, "logs/stale.log", "old")
	write(t, work, "cache/keep/x", "cached")

	runShell := func() {
		t.Helper()
		p := mustCompile(t, dir)
		if _, err := Run(Options{Root: dir, Pipeline: p, Jobs: p.Jobs, Executor: "shell", Stdout: os.Stdout, Stderr: os.Stderr}); err != nil {
			t.Fatal(err)
		}
	}
	exists := func(rel string) bool {
		_, err := os.Stat(filepath.Join(work, rel))
		return err == nil
	}

	runShell()
	if exists("logs/stale.log") {
		t.Fatal("stale log survived a new run")
	}
	if !exists("cache/keep/x") {
		t.Fatal("cache should be kept across runs")
	}
	if !exists("artifacts/old/a.txt") {
		t.Fatal("expected artifacts from this run")
	}
	if exists("tmp") {
		t.Fatal("tmp should be removed after the run")
	}
	if exists("builds") {
		t.Fatal("builds should be removed after the run")
	}

	write(t, dir, ".gitlab-ci.yml", `
new:
  script: echo second > b.txt
  artifacts:
    paths: [b.txt]
`)
	runShell()
	if exists("artifacts/old/a.txt") {
		t.Fatal("previous run artifacts should be cleared")
	}
	if !exists("artifacts/new/b.txt") {
		t.Fatal("expected artifacts from the second run")
	}
	if !exists("logs/new.log") {
		t.Fatal("expected log from the second run")
	}
}

func TestDebugKeepsBuildsAndTmp(t *testing.T) {
	dir := t.TempDir()
	work := filepath.Join(dir, ".glci")
	write(t, dir, ".gitlab-ci.yml", `
job:
  script: echo hello
  variables:
    SECRET_HINT: visible-in-debug
`)
	p := mustCompile(t, dir)
	opts := Options{Root: dir, Pipeline: p, Jobs: p.Jobs, Executor: "shell", Stdout: os.Stdout, Stderr: os.Stderr}
	if _, err := Run(opts); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(work, "tmp")); err == nil {
		t.Fatal("tmp should be removed when Debug is false")
	}
	if _, err := os.Stat(filepath.Join(work, "builds")); err == nil {
		t.Fatal("builds should be removed when Debug is false")
	}
	if _, err := os.Stat(filepath.Join(work, "builds", "job", ".glci", "job.json")); err == nil {
		t.Fatal("debug dumps must not remain when Debug is false")
	}

	opts.Debug = true
	if _, err := Run(opts); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(work, "tmp", "job.sh")
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("job script should remain with Debug: %v", err)
	}
	build := filepath.Join(work, "builds", "job")
	if st, err := os.Stat(build); err != nil || !st.IsDir() {
		t.Fatalf("build dir should remain with Debug: %v", err)
	}
	jobJSON := filepath.Join(build, ".glci", "job.json")
	if _, err := os.Stat(jobJSON); err != nil {
		t.Fatalf("expected job.json dump: %v", err)
	}
	varsEnv := filepath.Join(build, ".glci", "variables.env")
	b, err := os.ReadFile(varsEnv)
	if err != nil {
		t.Fatalf("expected variables.env dump: %v", err)
	}
	if !strings.Contains(string(b), "SECRET_HINT=visible-in-debug") {
		t.Fatalf("variables.env missing SECRET_HINT: %s", b)
	}
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
