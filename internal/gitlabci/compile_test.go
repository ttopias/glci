package gitlabci

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ttopias/glci/internal/gitctx"
)

func TestCompileBasic(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".gitlab-ci.yml", `
stages: [build, test]
variables:
  GREETING: hello

.hidden:
  script: echo hidden

build:
  stage: build
  script: echo $GREETING
  artifacts:
    paths: [out]
    reports:
      dotenv: build.env

test:
  stage: test
  needs: [build]
  script: echo test
  rules:
    - if: $CI_COMMIT_BRANCH == "main"
`)
	p := compileDir(t, dir, "main")
	if len(p.Jobs) != 2 {
		t.Fatalf("jobs=%d names=%v", len(p.Jobs), names(p.Jobs))
	}
	test := p.Jobs[1]
	if test.Name != "test" || !test.HasNeeds || test.Needs[0].Job != "build" {
		t.Fatalf("needs: %+v", test)
	}
}

func TestReferenceFromInclude(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "ci/common.yml", `
.setup:
  before_script:
    - echo setup-from-include
`)
	write(t, dir, ".gitlab-ci.yml", `
include:
  - local: ci/common.yml
build:
  extends: .setup
  script:
    - !reference [.setup, before_script]
    - echo hi
`)
	p := compileDir(t, dir, "main")
	if len(p.Jobs) != 1 {
		t.Fatalf("jobs=%v", names(p.Jobs))
	}
	t.Logf("script=%#v before=%#v raw=%#v", p.Jobs[0].Script, p.Jobs[0].BeforeScript, p.Jobs[0].Raw["script"])
	if len(p.Jobs[0].Script) < 1 || p.Jobs[0].Script[0] != "echo setup-from-include" {
		t.Fatalf("script=%v", p.Jobs[0].Script)
	}
}

func TestExtendsAndReference(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".gitlab-ci.yml", `
.setup:
  before_script:
    - echo setup

.rspec:
  extends: .setup
  script:
    - echo rspec

job:
  extends: .rspec
  script:
    - !reference [.setup, before_script]
    - echo job
`)
	p := compileDir(t, dir, "main")
	if len(p.Jobs) != 1 {
		t.Fatalf("jobs=%v", names(p.Jobs))
	}
	j := p.Jobs[0]
	if len(j.BeforeScript) != 1 || j.BeforeScript[0] != "echo setup" {
		t.Fatalf("before_script=%v", j.BeforeScript)
	}
	if len(j.Script) < 2 {
		t.Fatalf("script=%v", j.Script)
	}
}

func TestIncludeLocal(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "lib.yml", `
included:
  script: echo from-include
`)
	write(t, dir, ".gitlab-ci.yml", `
include:
  - local: lib.yml
root:
  script: echo root
`)
	p := compileDir(t, dir, "main")
	if len(p.Jobs) != 2 {
		t.Fatalf("jobs=%v", names(p.Jobs))
	}
}

func TestMatrixAndWorkflow(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".gitlab-ci.yml", `
workflow:
  rules:
    - if: $CI_PIPELINE_SOURCE == "web"
      when: never
    - when: always

test:
  parallel:
    matrix:
      - OS: [linux, mac]
  script: echo $OS
`)
	p := compileDir(t, dir, "main")
	if len(p.Jobs) != 2 {
		t.Fatalf("jobs=%v", names(p.Jobs))
	}
	p2, err := Compile(CompileOptions{
		Root: dir, File: ".gitlab-ci.yml",
		Git:    gitctx.Info{Root: dir, Branch: "main", Ref: "main", DefaultBranch: "main"},
		Source: "web",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(p2.Jobs) != 0 {
		t.Fatalf("workflow should drop pipeline, got %v", names(p2.Jobs))
	}
}

func TestRulesNever(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".gitlab-ci.yml", `
job:
  script: echo hi
  rules:
    - if: $CI_COMMIT_BRANCH == "dev"
`)
	p := compileDir(t, dir, "main")
	if len(p.Jobs) != 0 {
		t.Fatalf("expected no jobs, got %v", names(p.Jobs))
	}
}

func TestWhenManualNotOverridden(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".gitlab-ci.yml", `
deploy:
  script: echo deploy
  when: manual
`)
	p := compileDir(t, dir, "main")
	if len(p.Jobs) != 1 || p.Jobs[0].When != "manual" {
		t.Fatalf("when=%v jobs=%v", p.Jobs, names(p.Jobs))
	}
}

func TestColonInScript(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".gitlab-ci.yml", `
job:
  script:
    - echo "coverage: 42.0%"
    - echo hello: world
`)
	p := compileDir(t, dir, "main")
	if len(p.Jobs) != 1 {
		t.Fatalf("jobs=%v", names(p.Jobs))
	}
	got := p.Jobs[0].Script
	if got[0] != `echo "coverage: 42.0%"` {
		t.Fatalf("line0=%q", got[0])
	}
	if got[1] != "echo hello: world" {
		t.Fatalf("line1=%q", got[1])
	}
}

func TestSpecInputs(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "comp.yml", "spec:\n  inputs:\n    name:\n      default: world\n---\njob:\n  script: echo $[[ inputs.name ]]\n")
	write(t, dir, ".gitlab-ci.yml", "include:\n  - local: comp.yml\n")
	p := compileDir(t, dir, "main")
	if len(p.Jobs) != 1 || p.Jobs[0].Script[0] != "echo world" {
		t.Fatalf("got %+v", p.Jobs)
	}
}

func TestDefaultImageAndHooks(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".gitlab-ci.yml", `
default:
  image: python:3.12-alpine
  before_script: [echo def]
job:
  hooks:
    pre_get_sources_script:
      - echo hook
  script: echo hi
  cache:
    key:
      files: [go.mod]
      prefix: v1
    paths: [.cache]
`)
	p := compileDir(t, dir, "main")
	if len(p.Jobs) != 1 {
		t.Fatalf("jobs=%v", names(p.Jobs))
	}
	j := p.Jobs[0]
	if j.Image == nil || j.Image.Name != "python:3.12-alpine" {
		t.Fatalf("image=%+v", j.Image)
	}
	if len(j.PreGetSources) != 1 {
		t.Fatalf("hooks=%v", j.PreGetSources)
	}
	if len(j.Cache) != 1 || j.Cache[0].KeyPrefix != "v1" {
		t.Fatalf("cache=%+v", j.Cache)
	}
}

func TestTriggerInclude(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "child.yml", "child:\n  script: echo child\n")
	write(t, dir, ".gitlab-ci.yml", `
bridge:
  trigger:
    include: child.yml
    strategy: depend
`)
	p := compileDir(t, dir, "main")
	if len(p.Jobs) != 1 || p.Jobs[0].Trigger == nil {
		t.Fatalf("jobs=%+v", p.Jobs)
	}
	if p.Jobs[0].Trigger.Strategy != "depend" || len(p.Jobs[0].Trigger.LocalFiles) == 0 {
		t.Fatalf("trigger=%+v", p.Jobs[0].Trigger)
	}
}

func compileDir(t *testing.T, dir, branch string) *Pipeline {
	t.Helper()
	p, err := Compile(CompileOptions{
		Root: dir, File: ".gitlab-ci.yml",
		Git:    gitctx.Info{Root: dir, Branch: branch, Ref: branch, DefaultBranch: "main", SHA: "abc12345", ShortSHA: "abc12345"},
		Source: "push", DefaultImage: "alpine:3.20",
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
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

func names(jobs []Job) []string {
	var o []string
	for _, j := range jobs {
		o = append(o, j.Name)
	}
	return o
}
