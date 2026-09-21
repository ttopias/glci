package gitlabci

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ttopias/glci/internal/gitctx"
)

func TestRulesChangesExistsAndVariables(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "app.go", "package app\n")
	write(t, dir, ".gitlab-ci.yml", `
changed:
  script: echo changed
  rules:
    - if: $CI_COMMIT_BRANCH == "main"
      changes:
        - "*.go"
      variables:
        FROM_RULE: yes
exists_job:
  script: echo exists
  rules:
    - exists:
        - app.go
missing:
  script: echo no
  rules:
    - exists:
        - no-such-file
`)
	p, err := Compile(CompileOptions{
		Root: dir, File: ".gitlab-ci.yml",
		Git: gitctx.Info{
			Root: dir, Branch: "main", Ref: "main", DefaultBranch: "main",
			SHA: "abc12345", ShortSHA: "abc12345", ChangedFiles: []string{"app.go"},
		},
		Source: "push", DefaultImage: "alpine:3.20",
	})
	if err != nil {
		t.Fatal(err)
	}
	got := names(p.Jobs)
	if len(p.Jobs) != 2 {
		t.Fatalf("jobs=%v", got)
	}
	j, ok := JobByName(p.Jobs, "changed")
	if !ok || j.Variables["FROM_RULE"] != "yes" {
		t.Fatalf("changed=%+v", j)
	}
}

func TestIncludeRulesAndRemote(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "maybe.yml", "from_rules:\n  script: echo yes\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("from_remote:\n  script: echo remote\n"))
	}))
	t.Cleanup(srv.Close)
	write(t, dir, ".gitlab-ci.yml", `
include:
  - local: maybe.yml
    rules:
      - if: $CI_COMMIT_BRANCH == "main"
  - local: maybe.yml
    rules:
      - if: $CI_COMMIT_BRANCH == "other"
  - remote: `+srv.URL+`
root:
  script: echo root
`)
	p, err := Compile(CompileOptions{
		Root: dir, File: ".gitlab-ci.yml",
		Git:          gitctx.Info{Root: dir, Branch: "main", Ref: "main", DefaultBranch: "main", SHA: "abc12345", ShortSHA: "abc12345"},
		Source:       "push",
		DefaultImage: "alpine:3.20",
		AllowRemote:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, j := range p.Jobs {
		got[j.Name] = true
	}
	if !got["from_rules"] || !got["from_remote"] || !got["root"] {
		t.Fatalf("jobs=%v", names(p.Jobs))
	}
}

func TestRemoteIncludeBlocked(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".gitlab-ci.yml", "include:\n  - remote: https://example.invalid/ci.yml\njob:\n  script: echo x\n")
	_, err := Compile(CompileOptions{
		Root: dir, File: ".gitlab-ci.yml",
		Git:    gitctx.Info{Root: dir, Branch: "main", Ref: "main", DefaultBranch: "main"},
		Source: "push",
	})
	if err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("err=%v", err)
	}
}

func TestParallelIntegerNeedsOptionalAllowFailure(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".gitlab-ci.yml", `
stages: [build, test]
unit:
  stage: test
  parallel: 2
  script: echo $CI_NODE_INDEX
maybe:
  stage: test
  script: echo maybe
  needs:
    - job: missing
      optional: true
    - unit
  allow_failure: true
`)
	p := compileDir(t, dir, "main")
	if len(p.Jobs) != 3 {
		t.Fatalf("jobs=%v", names(p.Jobs))
	}
	var n int
	for _, j := range p.Jobs {
		if strings.HasPrefix(j.Name, "unit") {
			n++
			if j.ParallelTotal != 2 {
				t.Fatalf("parallel %+v", j)
			}
		}
		if j.Name == "maybe" {
			if !j.AllowFailure || !j.HasNeeds || len(j.Needs) != 2 || !j.Needs[0].Optional {
				t.Fatalf("maybe=%+v", j)
			}
		}
	}
	if n != 2 {
		t.Fatalf("unit copies=%d", n)
	}
}

func TestFilterJobsNeedsAndPrefix(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".gitlab-ci.yml", `
build:
  stage: build
  script: echo b
test:
  stage: test
  needs: [build]
  parallel:
    matrix:
      - TARGET: [unit, lint]
  script: echo $TARGET
`)
	p := compileDir(t, dir, "main")
	got := FilterJobs(p, []string{"test"})
	namesGot := names(got)
	if len(got) != 3 {
		t.Fatalf("filter=%v", namesGot)
	}
}

func TestRunKeywordAndCyclicExtends(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".gitlab-ci.yml", `
job:
  run:
    - name: step
      script: echo from-run
`)
	p := compileDir(t, dir, "main")
	if len(p.Jobs) != 1 || p.Jobs[0].Script[0] != "echo from-run" {
		t.Fatalf("run keyword %+v", p.Jobs)
	}

	write(t, dir, ".gitlab-ci.yml", `
.a:
  extends: .b
  script: echo a
.b:
  extends: .a
  script: echo b
job:
  extends: .a
  script: echo j
`)
	_, err := Compile(CompileOptions{
		Root: dir, File: ".gitlab-ci.yml",
		Git:    gitctx.Info{Root: dir, Branch: "main", Ref: "main", DefaultBranch: "main"},
		Source: "push",
	})
	if err == nil || !strings.Contains(err.Error(), "cyclic") {
		t.Fatalf("cyclic err=%v", err)
	}
}

func TestCompileExamples(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..")
	for _, rel := range []string{"examples/basic", "examples/full"} {
		dir := filepath.Join(root, rel)
		p, err := Compile(CompileOptions{
			Root: dir, File: ".gitlab-ci.yml",
			Git:          gitctx.Info{Root: dir, Branch: "main", Ref: "main", DefaultBranch: "main", SHA: "abc12345", ShortSHA: "abc12345"},
			Source:       "push",
			DefaultImage: "alpine:3.20",
		})
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		if len(p.Jobs) < 3 {
			t.Fatalf("%s jobs=%v", rel, names(p.Jobs))
		}
	}
}

func TestPredefinedVars(t *testing.T) {
	v := Predefined(RunContext{
		Git: gitctx.Info{
			Root: "/tmp/demo", Branch: "feat", Ref: "feat", SHA: "deadbeefdeadbeef",
			ShortSHA: "deadbeef", DefaultBranch: "main", Message: "hi", Title: "hi",
			RemoteURL: "https://gitlab.com/group/demo.git",
		},
		Source: "merge_request_event", Protected: true, MRSource: "feat", MRTarget: "main",
		PipelineID: "9", PipelineIID: "2",
	})
	if v["CI"] != "true" || v["GITLAB_CI"] != "true" {
		t.Fatalf("ci flags %v", v)
	}
	if v["CI_COMMIT_BRANCH"] != "feat" || v["CI_PIPELINE_SOURCE"] != "merge_request_event" {
		t.Fatalf("branch/source %q %q", v["CI_COMMIT_BRANCH"], v["CI_PIPELINE_SOURCE"])
	}
	if v["CI_PROJECT_NAME"] != "demo" {
		t.Fatalf("project=%q", v["CI_PROJECT_NAME"])
	}
	if v["CI_COMMIT_REF_PROTECTED"] != "true" {
		t.Fatalf("protected=%q", v["CI_COMMIT_REF_PROTECTED"])
	}
}

func TestLocalIncludeOutsideProject(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".gitlab-ci.yml", "include:\n  - local: ../secret.yml\njob:\n  script: echo x\n")
	_, err := Compile(CompileOptions{
		Root: dir, File: ".gitlab-ci.yml",
		Git:    gitctx.Info{Root: dir, Branch: "main", Ref: "main", DefaultBranch: "main"},
		Source: "push",
	})
	if err == nil || !(strings.Contains(err.Error(), "outside") || strings.Contains(err.Error(), "include:local")) {
		t.Fatalf("err=%v", err)
	}
}

func TestFilterJobsDoesNotMatchSiblingPrefix(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".gitlab-ci.yml", `
test:
  script: echo test
testimonial:
  script: echo no
`)
	p := compileDir(t, dir, "main")
	got := FilterJobs(p, []string{"test"})
	if len(got) != 1 || got[0].Name != "test" {
		t.Fatalf("got=%v", names(got))
	}
}
