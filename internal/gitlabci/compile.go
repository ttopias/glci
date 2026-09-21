package gitlabci

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ttopias/glci/internal/gitctx"
)

type Image struct {
	Name       string         `json:"name"`
	Entrypoint []string       `json:"entrypoint,omitempty"`
	PullPolicy string         `json:"pull_policy,omitempty"`
	Executor   string         `json:"executor,omitempty"`
	Docker     map[string]any `json:"docker,omitempty"`
}

type Service struct {
	Name       string            `json:"name"`
	Alias      string            `json:"alias,omitempty"`
	AliasList  []string          `json:"aliases,omitempty"`
	Command    []string          `json:"command,omitempty"`
	Entrypoint []string          `json:"entrypoint,omitempty"`
	Variables  map[string]string `json:"variables,omitempty"`
	PullPolicy string            `json:"pull_policy,omitempty"`
}

type Artifacts struct {
	Paths     []string       `json:"paths,omitempty"`
	Exclude   []string       `json:"exclude,omitempty"`
	Name      string         `json:"name,omitempty"`
	ExposeAs  string         `json:"expose_as,omitempty"`
	Untracked bool           `json:"untracked,omitempty"`
	When      string         `json:"when,omitempty"`
	ExpireIn  string         `json:"expire_in,omitempty"`
	Access    string         `json:"access,omitempty"`
	Public    *bool          `json:"public,omitempty"`
	Reports   map[string]any `json:"reports,omitempty"`
	Dotenv    []string       `json:"dotenv,omitempty"`
}

type Cache struct {
	Key       string   `json:"key,omitempty"`
	KeyFiles  []string `json:"key_files,omitempty"`
	KeyPrefix string   `json:"key_prefix,omitempty"`
	Fallback  []string `json:"fallback_keys,omitempty"`
	Paths     []string `json:"paths,omitempty"`
	Untracked bool     `json:"untracked,omitempty"`
	Unprotect bool     `json:"unprotect,omitempty"`
	When      string   `json:"when,omitempty"`
	Policy    string   `json:"policy,omitempty"`
}

type Need struct {
	Job       string            `json:"job"`
	Artifacts bool              `json:"artifacts"`
	Optional  bool              `json:"optional,omitempty"`
	Pipeline  string            `json:"pipeline,omitempty"`
	Project   string            `json:"project,omitempty"`
	Ref       string            `json:"ref,omitempty"`
	Parallel  map[string]string `json:"parallel,omitempty"`
}

type Retry struct {
	Max       int      `json:"max"`
	When      []string `json:"when,omitempty"`
	ExitCodes []int    `json:"exit_codes,omitempty"`
}

type Environment struct {
	Name           string `json:"name"`
	URL            string `json:"url,omitempty"`
	Action         string `json:"action,omitempty"`
	OnStop         string `json:"on_stop,omitempty"`
	AutoStopIn     string `json:"auto_stop_in,omitempty"`
	DeploymentTier string `json:"deployment_tier,omitempty"`
	Kubernetes     any    `json:"kubernetes,omitempty"`
}

type Trigger struct {
	Include    []includeSpec     `json:"-"`
	LocalFiles []string          `json:"include,omitempty"`
	Project    string            `json:"project,omitempty"`
	Branch     string            `json:"branch,omitempty"`
	Strategy   string            `json:"strategy,omitempty"`
	Forward    map[string]any    `json:"forward,omitempty"`
	Inputs     map[string]string `json:"inputs,omitempty"`
}

type Job struct {
	Name               string            `json:"name"`
	Stage              string            `json:"stage"`
	Script             []string          `json:"script,omitempty"`
	BeforeScript       []string          `json:"before_script,omitempty" yaml:"before_script,omitempty"`
	AfterScript        []string          `json:"after_script,omitempty" yaml:"after_script,omitempty"`
	Run                []any             `json:"run,omitempty"`
	Image              *Image            `json:"image,omitempty"`
	Services           []Service         `json:"services,omitempty"`
	Variables          map[string]string `json:"variables,omitempty"`
	Needs              []Need            `json:"needs,omitempty"`
	HasNeeds           bool              `json:"has_needs" yaml:"has_needs"`
	Dependencies       []string          `json:"dependencies,omitempty"`
	Artifacts          *Artifacts        `json:"artifacts,omitempty"`
	Cache              []Cache           `json:"cache,omitempty"`
	When               string            `json:"when"`
	AllowFailure       bool              `json:"allow_failure" yaml:"allow_failure"`
	AllowFailureCodes  []int             `json:"allow_failure_exit_codes,omitempty" yaml:"allow_failure_exit_codes,omitempty"`
	Retry              *Retry            `json:"retry,omitempty"`
	Timeout            time.Duration     `json:"timeout,omitempty"`
	Environment        *Environment      `json:"environment,omitempty"`
	Coverage           string            `json:"coverage,omitempty"`
	Tags               []string          `json:"tags,omitempty"`
	ResourceGroup      string            `json:"resource_group,omitempty"`
	Interruptible      bool              `json:"interruptible,omitempty"`
	StartIn            time.Duration     `json:"start_in,omitempty"`
	ManualConfirmation string            `json:"manual_confirmation,omitempty"`
	Release            map[string]any    `json:"release,omitempty"`
	Trigger            *Trigger          `json:"trigger,omitempty"`
	Pages              any               `json:"pages,omitempty"`
	Secrets            map[string]any    `json:"secrets,omitempty"`
	IDTokens           map[string]any    `json:"id_tokens,omitempty"`
	Identity           any               `json:"identity,omitempty"`
	Hooks              map[string]any    `json:"hooks,omitempty"`
	ParallelIndex      int               `json:"parallel_index,omitempty"`
	ParallelTotal      int               `json:"parallel_total,omitempty"`
	Matrix             map[string]string `json:"matrix,omitempty"`
	FileVariables      map[string]string `json:"file_variables,omitempty"`
	PreGetSources      []string          `json:"pre_get_sources_script,omitempty" yaml:"pre_get_sources_script,omitempty"`
	Publish            string            `json:"publish,omitempty"`
	Hidden             bool              `json:"-"`
	Raw                map[string]any    `json:"-"`
}

type Pipeline struct {
	Stages     []string          `json:"stages"`
	Jobs       []Job             `json:"jobs"`
	Variables  map[string]string `json:"variables"`
	Name       string            `json:"name,omitempty"`
	AutoCancel string            `json:"auto_cancel,omitempty"`
}

type CompileOptions struct {
	Root         string
	File         string
	Git          gitctx.Info
	Source       string
	ExtraVars    map[string]string
	Projects     map[string]string
	Components   map[string]string
	TemplatesDir string
	AllowRemote  bool
	Inputs       map[string]string
	Protected    bool
	OpenMRs      bool
	MRSource     string
	MRTarget     string
	PipelineID   string
	PipelineIID  string
	DefaultImage string
}

func Compile(opts CompileOptions) (*Pipeline, error) {
	if opts.PipelineID == "" {
		opts.PipelineID = NewID()
	}
	if opts.PipelineIID == "" {
		opts.PipelineIID = "1"
	}
	baseVars := Predefined(RunContext{
		Git: opts.Git, Source: opts.Source, Extra: opts.ExtraVars,
		Protected: opts.Protected, OpenMRs: opts.OpenMRs,
		MRSource: opts.MRSource, MRTarget: opts.MRTarget,
		PipelineID: opts.PipelineID, PipelineIID: opts.PipelineIID,
	})
	raw, err := Load(LoadOptions{
		Root: opts.Root, File: opts.File, Projects: opts.Projects,
		Components: opts.Components, TemplatesDir: opts.TemplatesDir,
		AllowRemote: opts.AllowRemote, Inputs: opts.Inputs, Vars: baseVars,
		EvalInclude: func(rules []any) bool {
			r := evalRules(rules, baseVars, opts.Git, opts.Root)
			return r.Match && r.When != "never"
		},
	})
	if err != nil {
		return nil, err
	}
	raw, err = resolveReferences(raw)
	if err != nil {
		return nil, err
	}
	raw, err = applyExtends(raw)
	if err != nil {
		return nil, err
	}

	globalVars := variablesFrom(raw["variables"])
	for k, v := range opts.ExtraVars {
		globalVars[k] = v
	}
	mergedVars := ExpandMap(mergeStr(baseVars, globalVars))

	if wf, ok := asMap(raw["workflow"]); ok {
		if rules := asList(wf["rules"]); len(rules) > 0 {
			r := evalRules(rules, mergedVars, opts.Git, opts.Root)
			if !r.Match || r.When == "never" {
				return &Pipeline{Stages: nil, Jobs: nil, Variables: mergedVars}, nil
			}
			for k, v := range r.Variables {
				mergedVars[k] = v
			}
		}
		if n := asString(wf["name"]); n != "" {
			mergedVars["CI_PIPELINE_NAME"] = Expand(n, mergedVars)
		}
	}

	defaults := map[string]any{}
	if d, ok := asMap(raw["default"]); ok {
		defaults = d
	}
	// Deprecated global keywords behave like default.
	for _, k := range []string{"image", "services", "cache", "before_script", "after_script", "retry", "timeout", "interruptible", "hooks", "id_tokens", "artifacts"} {
		if _, ok := defaults[k]; !ok {
			if v, exists := raw[k]; exists && k != "artifacts" {
				defaults[k] = v
			}
		}
	}

	stages := []string{".pre", "build", "test", "deploy", ".post"}
	if raw["stages"] != nil {
		user := asStringSlice(raw["stages"])
		stages = append([]string{".pre"}, user...)
		if !contains(stages, ".post") {
			stages = append(stages, ".post")
		}
		if !contains(stages, ".pre") {
			stages = append([]string{".pre"}, stages...)
		}
	}

	var jobs []Job
	for _, name := range sortedKeys(raw) {
		if reserved[name] && name != "pages" {
			continue
		}
		body, ok := asMap(raw[name])
		if !ok {
			continue
		}
		if isHidden(name) {
			continue
		}
		expanded, err := expandJob(name, body, defaults, mergedVars, opts)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, expanded...)
	}

	// Drop jobs whose stage isn't in stages, except trigger/bridge still count.
	stageSet := map[string]bool{}
	for _, s := range stages {
		stageSet[s] = true
	}
	var kept []Job
	for _, j := range jobs {
		if !stageSet[j.Stage] {
			stages = append(stages, j.Stage)
			stageSet[j.Stage] = true
		}
		kept = append(kept, j)
	}

	p := &Pipeline{
		Stages:    stages,
		Jobs:      kept,
		Variables: mergedVars,
		Name:      mergedVars["CI_PIPELINE_NAME"],
	}
	if err := validateNeeds(p); err != nil {
		return nil, err
	}
	return p, nil
}

func expandJob(name string, body, defaults map[string]any, vars map[string]string, opts CompileOptions) ([]Job, error) {
	jobVars := mergeStr(vars, variablesFrom(body["variables"]))
	inherit := asMapOrEmpty(body["inherit"])
	if spec, ok := inherit["variables"]; ok {
		jobVars = filterInheritedVars(vars, spec, variablesFrom(body["variables"]), opts, name)
	}
	body = applyDefaults(body, defaults, inherit["default"])

	if !evalOnlyExcept(body, jobVars, opts.Git) {
		return nil, nil
	}
	when := asString(body["when"])
	rr := ruleResult{Match: true, When: when}
	if body["rules"] != nil {
		rr = evalRules(asList(body["rules"]), jobVars, opts.Git, opts.Root)
		if !rr.Match || rr.When == "never" {
			return nil, nil
		}
		if rr.When != "" {
			when = rr.When
		}
	}
	if when == "" {
		when = "on_success"
	}
	for k, v := range rr.Variables {
		jobVars[k] = v
	}
	if rr.Needs != nil {
		body["needs"] = rr.Needs
	}

	matrixJobs := expandMatrix(name, body)
	out := make([]Job, 0, len(matrixJobs))
	for _, mj := range matrixJobs {
		jvars := mergeStr(jobVars, mj.vars)
		jvars = ExpandMap(jvars)
		j, err := jobFrom(mj.name, body, jvars, when, rr, opts)
		if err != nil {
			return nil, err
		}
		j.ParallelIndex = mj.index
		j.ParallelTotal = mj.total
		j.Matrix = mj.vars
		if j.Image == nil || j.Image.Name == "" {
			img := opts.DefaultImage
			if img == "" {
				img = "alpine:3.20"
			}
			j.Image = &Image{Name: img}
		}
		if len(j.Script) == 0 && len(j.Run) > 0 {
			j.Script = scriptsFromRun(j.Run, j.Variables)
		}
		if j.Script == nil && j.Trigger == nil && j.Run == nil && j.Pages == nil {
			return nil, fmt.Errorf("job %q is missing script, run, or trigger", name)
		}
		out = append(out, j)
	}
	return out, nil
}

type matrixJob struct {
	name  string
	vars  map[string]string
	index int
	total int
}

func expandMatrix(name string, body map[string]any) []matrixJob {
	par := body["parallel"]
	if par == nil {
		return []matrixJob{{name: name, vars: nil, index: 1, total: 1}}
	}
	if n := asInt(par, 0); n > 0 {
		out := make([]matrixJob, n)
		for i := 0; i < n; i++ {
			out[i] = matrixJob{
				name:  fmt.Sprintf("%s %d/%d", name, i+1, n),
				index: i + 1, total: n,
			}
		}
		return out
	}
	m, ok := asMap(par)
	if !ok {
		return []matrixJob{{name: name, index: 1, total: 1}}
	}
	rows := asList(m["matrix"])
	var combos []map[string]string
	for _, row := range rows {
		rm, ok := asMap(row)
		if !ok {
			continue
		}
		combos = append(combos, cartesian(rm)...)
	}
	if len(combos) == 0 {
		return []matrixJob{{name: name, index: 1, total: 1}}
	}
	out := make([]matrixJob, len(combos))
	for i, c := range combos {
		keys := make([]string, 0, len(c))
		for k := range c {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		vals := make([]string, 0, len(keys))
		for _, k := range keys {
			vals = append(vals, c[k])
		}
		out[i] = matrixJob{
			name:  fmt.Sprintf("%s: [%s]", name, strings.Join(vals, ", ")),
			vars:  c,
			index: i + 1,
			total: len(combos),
		}
	}
	return out
}

func cartesian(row map[string]any) []map[string]string {
	keys := sortedKeys(row)
	var lists [][]string
	for _, k := range keys {
		vals := asStringSlice(row[k])
		if len(vals) == 0 {
			vals = []string{""}
		}
		lists = append(lists, vals)
	}
	var out []map[string]string
	var rec func(i int, cur map[string]string)
	rec = func(i int, cur map[string]string) {
		if i == len(keys) {
			cp := make(map[string]string, len(cur))
			for k, v := range cur {
				cp[k] = v
			}
			out = append(out, cp)
			return
		}
		for _, v := range lists[i] {
			cur[keys[i]] = v
			rec(i+1, cur)
		}
	}
	rec(0, map[string]string{})
	return out
}

func jobFrom(name string, body map[string]any, vars map[string]string, when string, rr ruleResult, opts CompileOptions) (Job, error) {
	stage := asString(body["stage"])
	if stage == "" {
		stage = "test"
	}
	j := Job{
		Name:               Expand(name, vars),
		Stage:              Expand(stage, vars),
		Script:             expandScripts(body["script"], vars),
		BeforeScript:       expandScripts(body["before_script"], vars),
		AfterScript:        expandScripts(body["after_script"], vars),
		Run:                asList(ExpandAny(body["run"], vars)),
		Variables:          vars,
		When:               when,
		Coverage:           Expand(asString(body["coverage"]), vars),
		Tags:               expandScripts(body["tags"], vars),
		ResourceGroup:      Expand(asString(body["resource_group"]), vars),
		Interruptible:      asBool(body["interruptible"], false),
		ManualConfirmation: asString(body["manual_confirmation"]),
		Secrets:            asMapOrEmpty(body["secrets"]),
		IDTokens:           asMapOrEmpty(body["id_tokens"]),
		Identity:           body["identity"],
		Hooks:              asMapOrEmpty(body["hooks"]),
		Release:            asMapOrEmpty(body["release"]),
		Pages:              body["pages"],
		FileVariables:      fileVariables(body["variables"]),
		Raw:                body,
	}
	if h := asMapOrEmpty(body["hooks"]); len(h) > 0 {
		j.PreGetSources = expandScripts(h["pre_get_sources_script"], vars)
	}
	if name == "pages" && j.Pages == nil {
		j.Pages = true
	}
	if rr.AllowFailure != nil {
		j.AllowFailure = *rr.AllowFailure
	} else {
		j.AllowFailure = parseAllowFailure(body["allow_failure"], &j)
	}
	if rr.Interruptible != nil {
		j.Interruptible = *rr.Interruptible
	}
	j.Image = parseImage(body["image"], vars)
	j.Services = parseServices(body["services"], vars)
	j.Artifacts = parseArtifacts(body["artifacts"], vars)
	if pm, ok := asMap(body["pages"]); ok {
		j.Publish = Expand(asString(first(pm["publish"], pm["path"])), vars)
		if j.Publish != "" {
			if j.Artifacts == nil {
				j.Artifacts = &Artifacts{When: "on_success"}
			}
			j.Artifacts.Paths = append(j.Artifacts.Paths, j.Publish)
		}
	}
	j.Cache = parseCaches(body["cache"], vars)
	j.Retry = parseRetry(body["retry"])
	j.Timeout = parseDuration(asString(body["timeout"]))
	startIn := asString(body["start_in"])
	if rr.StartIn != "" {
		startIn = rr.StartIn
	}
	j.StartIn = parseDuration(startIn)
	j.Environment = parseEnvironment(body["environment"], vars)
	j.Needs, j.HasNeeds = parseNeeds(body)
	if deps, ok := body["dependencies"]; ok {
		j.Dependencies = asStringSlice(deps)
	}
	if t := body["trigger"]; t != nil {
		j.Trigger = parseTrigger(t)
	}
	vars["CI_JOB_NAME"] = j.Name
	vars["CI_JOB_STAGE"] = j.Stage
	if j.ParallelTotal > 1 {
		vars["CI_NODE_INDEX"] = fmt.Sprint(j.ParallelIndex)
		vars["CI_NODE_TOTAL"] = fmt.Sprint(j.ParallelTotal)
	}
	return j, nil
}

func expandScripts(v any, vars map[string]string) []string {
	out := asStringSlice(v)
	for i, s := range out {
		out[i] = Expand(s, vars)
	}
	return out
}

func parseImage(v any, vars map[string]string) *Image {
	if v == nil {
		return nil
	}
	if s, ok := v.(string); ok {
		return &Image{Name: Expand(s, vars)}
	}
	m, ok := asMap(v)
	if !ok {
		return nil
	}
	img := &Image{
		Name:       Expand(asString(m["name"]), vars),
		Entrypoint: asStringSlice(m["entrypoint"]),
		PullPolicy: asString(first(m["pull_policy"], m["pull-policy"])),
		Executor:   asString(m["executor"]),
	}
	if d, ok := asMap(m["docker"]); ok {
		img.Docker = d
	}
	return img
}

func parseServices(v any, vars map[string]string) []Service {
	var out []Service
	for _, item := range asList(v) {
		if s, ok := item.(string); ok {
			out = append(out, Service{Name: Expand(s, vars), Alias: serviceAlias(s)})
			continue
		}
		m, ok := asMap(item)
		if !ok {
			continue
		}
		name := Expand(asString(m["name"]), vars)
		svc := Service{
			Name:       name,
			Alias:      Expand(asString(m["alias"]), vars),
			AliasList:  asStringSlice(m["alias"]),
			Command:    asStringSlice(m["command"]),
			Entrypoint: asStringSlice(m["entrypoint"]),
			Variables:  variablesFrom(m["variables"]),
			PullPolicy: asString(m["pull_policy"]),
		}
		if svc.Alias == "" {
			svc.Alias = serviceAlias(name)
		}
		out = append(out, svc)
	}
	return out
}

func serviceAlias(image string) string {
	s := image
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	if i := strings.Index(s, ":"); i >= 0 {
		s = s[:i]
	}
	return s
}

func parseArtifacts(v any, vars map[string]string) *Artifacts {
	m, ok := asMap(v)
	if !ok {
		return nil
	}
	a := &Artifacts{
		Paths:     expandScripts(m["paths"], vars),
		Exclude:   expandScripts(m["exclude"], vars),
		Name:      Expand(asString(m["name"]), vars),
		ExposeAs:  asString(m["expose_as"]),
		Untracked: asBool(m["untracked"], false),
		When:      asString(m["when"]),
		ExpireIn:  asString(m["expire_in"]),
		Access:    asString(m["access"]),
		Reports:   asMapOrEmpty(m["reports"]),
	}
	if v, ok := m["public"]; ok {
		b := asBool(v, true)
		a.Public = &b
	}
	if r, ok := asMap(m["reports"]); ok {
		a.Dotenv = asStringSlice(r["dotenv"])
	}
	if a.When == "" {
		a.When = "on_success"
	}
	return a
}

func parseCaches(v any, vars map[string]string) []Cache {
	if v == nil {
		return nil
	}
	var items []any
	if _, ok := asMap(v); ok {
		items = []any{v}
	} else {
		items = asList(v)
	}
	var out []Cache
	for _, item := range items {
		m, ok := asMap(item)
		if !ok {
			continue
		}
		c := Cache{
			Paths:     expandScripts(m["paths"], vars),
			Untracked: asBool(m["untracked"], false),
			Unprotect: asBool(m["unprotect"], false),
			When:      asString(m["when"]),
			Policy:    asString(m["policy"]),
			Fallback:  asStringSlice(m["fallback_keys"]),
		}
		if c.Policy == "" {
			c.Policy = "pull-push"
		}
		if c.When == "" {
			c.When = "on_success"
		}
		switch k := m["key"].(type) {
		case string:
			c.Key = Expand(k, vars)
		default:
			if km, ok := asMap(k); ok {
				c.KeyFiles = expandScripts(km["files"], vars)
				c.KeyPrefix = Expand(asString(km["prefix"]), vars)
				c.Key = strings.Join(c.KeyFiles, "-")
				if c.KeyPrefix != "" {
					c.Key = c.KeyPrefix + "-" + c.Key
				}
			}
		}
		if c.Key == "" {
			c.Key = "default"
		}
		out = append(out, c)
	}
	return out
}

func parseRetry(v any) *Retry {
	if v == nil {
		return nil
	}
	if n := asInt(v, -1); n >= 0 && (v != nil) {
		if _, isMap := asMap(v); !isMap {
			return &Retry{Max: n, When: defaultRetryWhen()}
		}
	}
	m, ok := asMap(v)
	if !ok {
		return nil
	}
	r := &Retry{Max: asInt(m["max"], 0), When: asStringSlice(m["when"])}
	if len(r.When) == 0 {
		r.When = defaultRetryWhen()
	}
	for _, c := range asList(m["exit_codes"]) {
		r.ExitCodes = append(r.ExitCodes, asInt(c, 0))
	}
	return r
}

func defaultRetryWhen() []string {
	return []string{
		"unknown_failure", "api_failure", "stuck_or_timeout_failure",
		"runner_system_failure", "stale_schedule", "runner_unsupported",
	}
}

func parseEnvironment(v any, vars map[string]string) *Environment {
	if v == nil {
		return nil
	}
	if s, ok := v.(string); ok {
		return &Environment{Name: Expand(s, vars)}
	}
	m, ok := asMap(v)
	if !ok {
		return nil
	}
	return &Environment{
		Name:           Expand(asString(m["name"]), vars),
		URL:            Expand(asString(m["url"]), vars),
		Action:         asString(m["action"]),
		OnStop:         asString(m["on_stop"]),
		AutoStopIn:     asString(m["auto_stop_in"]),
		DeploymentTier: asString(m["deployment_tier"]),
		Kubernetes:     m["kubernetes"],
	}
}

func parseNeeds(body map[string]any) ([]Need, bool) {
	v, ok := body["needs"]
	if !ok {
		return nil, false
	}
	var out []Need
	for _, item := range asList(v) {
		if s, ok := item.(string); ok {
			out = append(out, Need{Job: s, Artifacts: true})
			continue
		}
		m, ok := asMap(item)
		if !ok {
			continue
		}
		n := Need{
			Job:       asString(m["job"]),
			Artifacts: asBool(m["artifacts"], true),
			Optional:  asBool(m["optional"], false),
			Pipeline:  asString(m["pipeline"]),
			Project:   asString(m["project"]),
			Ref:       asString(m["ref"]),
		}
		if pm, ok := asMap(m["parallel"]); ok {
			if mx, ok := asMap(pm["matrix"]); ok {
				n.Parallel = stringMap(mx)
			}
		}
		out = append(out, n)
	}
	return out, true
}

func parseTrigger(v any) *Trigger {
	if s, ok := v.(string); ok {
		return &Trigger{Project: s}
	}
	m, ok := asMap(v)
	if !ok {
		return nil
	}
	t := &Trigger{
		Project:  asString(m["project"]),
		Branch:   asString(m["branch"]),
		Strategy: asString(m["strategy"]),
		Forward:  asMapOrEmpty(m["forward"]),
		Inputs:   stringMap(m["inputs"]),
	}
	if inc, err := normalizeIncludes(m["include"]); err == nil {
		t.Include = inc
		for _, i := range inc {
			if i.Local != "" {
				t.LocalFiles = append(t.LocalFiles, i.Local)
			}
			t.LocalFiles = append(t.LocalFiles, i.File...)
		}
	}
	return t
}

func parseAllowFailure(v any, j *Job) bool {
	if v == nil {
		return false
	}
	if _, ok := v.(bool); ok {
		return asBool(v, false)
	}
	m, ok := asMap(v)
	if !ok {
		return asBool(v, false)
	}
	for _, c := range asList(m["exit_codes"]) {
		j.AllowFailureCodes = append(j.AllowFailureCodes, asInt(c, 0))
	}
	return false
}

func parseDuration(s string) time.Duration {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	if d, err := time.ParseDuration(strings.ReplaceAll(s, " ", "")); err == nil {
		return d
	}
	var n int
	var unit string
	if _, err := fmt.Sscanf(s, "%d %s", &n, &unit); err == nil {
		unit = strings.TrimSuffix(strings.ToLower(unit), "s")
		switch {
		case strings.HasPrefix(unit, "sec"):
			return time.Duration(n) * time.Second
		case strings.HasPrefix(unit, "min"):
			return time.Duration(n) * time.Minute
		case strings.HasPrefix(unit, "hour"), strings.HasPrefix(unit, "hr"):
			return time.Duration(n) * time.Hour
		case strings.HasPrefix(unit, "day"):
			return time.Duration(n) * 24 * time.Hour
		case strings.HasPrefix(unit, "week"):
			return time.Duration(n) * 7 * 24 * time.Hour
		}
	}
	if d, err := time.ParseDuration(s); err == nil {
		return d
	}
	return 0
}

func applyDefaults(job, defaults map[string]any, inheritDefault any) map[string]any {
	out := clone(job).(map[string]any)
	for _, k := range keysToInherit(defaults, inheritDefault) {
		if _, ok := out[k]; !ok {
			out[k] = clone(defaults[k])
		}
	}
	return out
}

func keysToInherit(src map[string]any, spec any) []string {
	if spec == nil {
		return sortedKeys(src)
	}
	if b, ok := spec.(bool); ok {
		if !b {
			return nil
		}
		return sortedKeys(src)
	}
	want := map[string]bool{}
	for _, k := range asStringSlice(spec) {
		want[k] = true
	}
	var keys []string
	for _, k := range sortedKeys(src) {
		if want[k] {
			keys = append(keys, k)
		}
	}
	return keys
}

func filterInheritedVars(all map[string]string, spec any, job map[string]string, opts CompileOptions, name string) map[string]string {
	base := Predefined(RunContext{
		Git: opts.Git, Source: opts.Source, Extra: opts.ExtraVars,
		Protected: opts.Protected, PipelineID: opts.PipelineID, PipelineIID: opts.PipelineIID,
		JobName: name,
	})
	if b, ok := spec.(bool); ok && !b {
		return mergeStr(base, job)
	}
	out := mergeStr(base, nil)
	for _, k := range asStringSlice(spec) {
		if v, ok := all[k]; ok {
			out[k] = v
		}
	}
	return mergeStr(out, job)
}

func fileVariables(v any) map[string]string {
	m, ok := asMap(v)
	if !ok {
		return nil
	}
	out := map[string]string{}
	for k, val := range m {
		nested, ok := asMap(val)
		if !ok {
			continue
		}
		if asBool(nested["file"], false) {
			out[k] = asString(nested["value"])
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func scriptsFromRun(run []any, vars map[string]string) []string {
	var out []string
	for _, step := range run {
		m, ok := asMap(step)
		if !ok {
			continue
		}
		out = append(out, expandScripts(m["script"], vars)...)
	}
	return out
}

func applyExtends(root map[string]any) (map[string]any, error) {
	out := clone(root).(map[string]any)
	resolving := map[string]bool{}
	var resolve func(name string) (map[string]any, error)
	resolve = func(name string) (map[string]any, error) {
		body, ok := asMap(out[name])
		if !ok {
			return nil, fmt.Errorf("extends %q: not found", name)
		}
		if resolving[name] {
			return nil, fmt.Errorf("cyclic extends involving %q", name)
		}
		ext := body["extends"]
		if ext == nil {
			return body, nil
		}
		resolving[name] = true
		parents := asStringSlice(ext)
		merged := map[string]any{}
		for _, p := range parents {
			pb, err := resolve(p)
			if err != nil {
				return nil, err
			}
			merged = mergeConfig(merged, pb)
		}
		delete(body, "extends")
		out[name] = mergeConfig(merged, body)
		delete(resolving, name)
		return asMapOrEmpty(out[name]), nil
	}
	for _, name := range sortedKeys(out) {
		if _, ok := asMap(out[name]); !ok {
			continue
		}
		if _, err := resolve(name); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func validateNeeds(p *Pipeline) error {
	names := map[string]Job{}
	for _, j := range p.Jobs {
		names[j.Name] = j
	}
	stageIdx := map[string]int{}
	for i, s := range p.Stages {
		stageIdx[s] = i
	}
	for _, j := range p.Jobs {
		for _, n := range j.Needs {
			if n.Job == "" || n.Pipeline != "" || n.Project != "" {
				continue
			}
			dep, ok := names[n.Job]
			if !ok {
				// maybe parallel parent name
				found := false
				for _, other := range p.Jobs {
					if strings.HasPrefix(other.Name, n.Job) {
						found = true
						break
					}
				}
				if !found && !n.Optional {
					return fmt.Errorf("job %q needs unknown job %q", j.Name, n.Job)
				}
				continue
			}
			if stageIdx[dep.Stage] > stageIdx[j.Stage] {
				return fmt.Errorf("job %q needs %q from a later stage", j.Name, n.Job)
			}
		}
	}
	return nil
}

func mergeStr(a, b map[string]string) map[string]string {
	out := make(map[string]string, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

func asMapOrEmpty(v any) map[string]any {
	m, _ := asMap(v)
	if m == nil {
		return map[string]any{}
	}
	return m
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func first(vs ...any) any {
	for _, v := range vs {
		if v != nil {
			return v
		}
	}
	return nil
}

func JobByName(jobs []Job, name string) (Job, bool) {
	for _, j := range jobs {
		if j.Name == name {
			return j, true
		}
	}
	return Job{}, false
}

func FilterJobs(p *Pipeline, names []string) []Job {
	if len(names) == 0 {
		return p.Jobs
	}
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	byName := map[string]Job{}
	for _, j := range p.Jobs {
		byName[j.Name] = j
	}
	var out []Job
	seen := map[string]bool{}
	var add func(string)
	add = func(n string) {
		if seen[n] {
			return
		}
		j, ok := byName[n]
		if !ok {
			for _, other := range p.Jobs {
				if MatchJobName(other.Name, n) {
					add(other.Name)
				}
			}
			return
		}
		seen[n] = true
		if j.HasNeeds {
			for _, nd := range j.Needs {
				if nd.Job != "" {
					add(nd.Job)
				}
			}
		} else {
			si := indexOf(p.Stages, j.Stage)
			for _, other := range p.Jobs {
				if indexOf(p.Stages, other.Stage) < si {
					add(other.Name)
				}
			}
		}
		if j.Dependencies != nil {
			for _, d := range j.Dependencies {
				add(d)
			}
		}
		out = append(out, j)
	}
	for _, n := range names {
		add(n)
	}
	return out
}

func indexOf(ss []string, s string) int {
	for i, x := range ss {
		if x == s {
			return i
		}
	}
	return 0
}
