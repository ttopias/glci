package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ttopias/glci/internal/gitctx"
	"github.com/ttopias/glci/internal/gitlabci"
)

func TestDotenvAndPages(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".gitlab-ci.yml", `
stages: [build, test]
build:
  stage: build
  script:
    - echo FOO=from-dotenv > build.env
    - mkdir -p public
    - echo hi > public/index.html
  artifacts:
    paths: [build.env]
    reports:
      dotenv: build.env
  pages: true
test:
  stage: test
  needs: [build]
  script:
    - test "$FOO" = "from-dotenv"
`)
	p := mustCompile(t, dir)
	if _, err := Run(Options{Root: dir, Pipeline: p, Jobs: p.Jobs, Executor: "shell", Stdout: os.Stdout, Stderr: os.Stderr}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".glci", "pages", "index.html")); err != nil {
		t.Fatalf("pages: %v", err)
	}
}

func TestCacheRestore(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".gitlab-ci.yml", `
one:
  cache:
    key: k1
    paths: [cached]
  script:
    - mkdir -p cached
    - echo 1 > cached/a
two:
  needs: [one]
  cache:
    key: k1
    paths: [cached]
  script:
    - test -f cached/a
`)
	p := mustCompile(t, dir)
	if _, err := Run(Options{Root: dir, Pipeline: p, Jobs: p.Jobs, Executor: "shell", Stdout: os.Stdout, Stderr: os.Stderr}); err != nil {
		t.Fatal(err)
	}
}

func TestFileVarsSecretsIDTokensAfterScript(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GLCI_SECRET_VAULT_TOKEN", "from-env")
	write(t, dir, ".gitlab-ci.yml", `
ok:
  script:
    - grep secret-contents "$KEYFILE"
    - test "$VAULT_TOKEN" = "from-env"
    - test -n "$MY_TOKEN"
    - exit 1
  after_script:
    - echo AFTER_RAN
  variables:
    KEYFILE:
      value: secret-contents
      file: true
  secrets:
    VAULT_TOKEN:
      vault: production/token
  id_tokens:
    MY_TOKEN:
      aud: https://example.com
  allow_failure: true
`)
	p := mustCompile(t, dir)
	res, err := Run(Options{Root: dir, Pipeline: p, Jobs: p.Jobs, Executor: "shell", Stdout: os.Stdout, Stderr: os.Stderr})
	if err != nil {
		t.Fatalf("allow_failure should not fail pipeline: %v", err)
	}
	if res[0].Status != "failed-allowed" {
		t.Fatalf("status=%s", res[0].Status)
	}
	logb, _ := os.ReadFile(res[0].LogPath)
	if !strings.Contains(string(logb), "AFTER_RAN") {
		t.Fatalf("after_script missing in log: %s", logb)
	}
}

func TestOnFailureAndRetry(t *testing.T) {
	dir := t.TempDir()
	flag := filepath.Join(dir, "retry.flag")
	write(t, dir, ".gitlab-ci.yml", `
stages: [build, recover]
boom:
  stage: build
  variables:
    FLAG: "`+flag+`"
  script:
    - if [ -f "$FLAG" ]; then echo recovered; else echo x > "$FLAG"; exit 1; fi
  retry:
    max: 1
    when: [script_failure]
fix:
  stage: recover
  when: on_failure
  script: echo fixed
  needs: [boom]
`)
	p := mustCompile(t, dir)
	res, err := Run(Options{Root: dir, Pipeline: p, Jobs: p.Jobs, Executor: "shell", Stdout: os.Stdout, Stderr: os.Stderr})
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]Result{}
	for _, r := range res {
		by[r.Name] = r
	}
	if by["boom"].Status != "success" {
		t.Fatalf("retry should succeed, boom=%+v", by["boom"])
	}
	if !by["fix"].Skipped {
		t.Fatalf("on_failure should skip when boom succeeded, got %+v", by["fix"])
	}
}

func TestOnFailureRuns(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".gitlab-ci.yml", `
fail:
  script: exit 1
cleanup:
  when: on_failure
  script: echo cleaned
  needs: [fail]
`)
	p := mustCompile(t, dir)
	res, err := Run(Options{Root: dir, Pipeline: p, Jobs: p.Jobs, Executor: "shell", Stdout: os.Stdout, Stderr: os.Stderr})
	if err == nil {
		t.Fatal("expected pipeline failure")
	}
	by := map[string]Result{}
	for _, r := range res {
		by[r.Name] = r
	}
	if by["cleanup"].Status != "success" {
		t.Fatalf("cleanup=%+v", by["cleanup"])
	}
}

func TestChildPipelineTrigger(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "child.yml", "kid:\n  script: echo child-ok\n")
	write(t, dir, ".gitlab-ci.yml", `
bridge:
  trigger:
    include: child.yml
    strategy: depend
`)
	p := mustCompile(t, dir)
	res, err := Run(Options{
		Root: dir, Pipeline: p, Jobs: p.Jobs, Executor: "shell",
		Stdout: os.Stdout, Stderr: os.Stderr,
		Compile: gitlabci.CompileOptions{
			Root: dir, Git: gitctx.Info{Root: dir, Branch: "main", Ref: "main", DefaultBranch: "main"},
			Source: "push", DefaultImage: "alpine:3.24",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Status != "success" {
		t.Fatalf("bridge=%+v", res)
	}
}

func TestResourceGroupSerial(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".gitlab-ci.yml", `
a:
  script: echo a
  resource_group: prod
  needs: []
b:
  script: echo b
  resource_group: prod
  needs: []
`)
	p := mustCompile(t, dir)
	res, err := Run(Options{Root: dir, Pipeline: p, Jobs: p.Jobs, Executor: "shell", Concurrency: 2, Stdout: os.Stdout, Stderr: os.Stderr})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 || res[0].Status != "success" || res[1].Status != "success" {
		t.Fatalf("res=%+v", res)
	}
}

func TestWhenNever(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".gitlab-ci.yml", `
keep:
  script: echo yes
drop:
  script: echo no
  rules:
    - when: never
`)
	p := mustCompile(t, dir)
	res, err := Run(Options{Root: dir, Pipeline: p, Jobs: p.Jobs, Executor: "shell", Stdout: os.Stdout, Stderr: os.Stderr})
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]Result{}
	for _, r := range res {
		by[r.Name] = r
	}
	if by["keep"].Status != "success" {
		t.Fatalf("keep=%+v", by["keep"])
	}
	if drop, ok := by["drop"]; ok && !drop.Skipped {
		t.Fatalf("drop should skip, got %+v", drop)
	}
}

func TestManualNeedSkipsDownstream(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".gitlab-ci.yml", `
manual_job:
  when: manual
  script: echo m
later:
  needs: [manual_job]
  script: echo later
`)
	p := mustCompile(t, dir)
	res, err := Run(Options{Root: dir, Pipeline: p, Jobs: p.Jobs, Executor: "shell", Stdout: os.Stdout, Stderr: os.Stderr})
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]Result{}
	for _, r := range res {
		by[r.Name] = r
	}
	if !by["manual_job"].Skipped || !by["later"].Skipped {
		t.Fatalf("expected both skipped, got %+v", by)
	}
}

func TestArtifactsWhenOnSuccess(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".gitlab-ci.yml", `
failing:
  script:
    - echo leftover > keep.txt
    - exit 1
  artifacts:
    paths: [keep.txt]
`)
	p := mustCompile(t, dir)
	_, err := Run(Options{Root: dir, Pipeline: p, Jobs: p.Jobs, Executor: "shell", Stdout: os.Stdout, Stderr: os.Stderr})
	if err == nil {
		t.Fatal("expected failure")
	}
	if _, err := os.Stat(filepath.Join(dir, ".glci", "artifacts", "failing", "keep.txt")); err == nil {
		t.Fatal("default artifacts.when=on_success should not keep failed artifacts")
	}
}

func TestArtifactPathEscape(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(filepath.Dir(dir), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(outside) })
	write(t, dir, ".gitlab-ci.yml", `
job:
  script: echo ok
  artifacts:
    paths: ["../secret.txt"]
`)
	p := mustCompile(t, dir)
	_, err := Run(Options{Root: dir, Pipeline: p, Jobs: p.Jobs, Executor: "shell", Stdout: os.Stdout, Stderr: os.Stderr})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".glci", "artifacts", "job", "secret.txt")); err == nil {
		t.Fatal("escaped artifact path was copied")
	}
}

func TestChildPipelineSource(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "child.yml", `
kid:
  script: test "$CI_PIPELINE_SOURCE" = "parent_pipeline"
  rules:
    - if: $CI_PIPELINE_SOURCE == "parent_pipeline"
`)
	write(t, dir, ".gitlab-ci.yml", `
bridge:
  trigger:
    include: child.yml
    strategy: depend
`)
	write(t, dir, ".glci/cache/keep/x", "cached")
	p := mustCompile(t, dir)
	res, err := Run(Options{
		Root: dir, Pipeline: p, Jobs: p.Jobs, Executor: "shell",
		Stdout: os.Stdout, Stderr: os.Stderr,
		Compile: gitlabci.CompileOptions{
			Root: dir, Git: gitctx.Info{Root: dir, Branch: "main", Ref: "main", DefaultBranch: "main", SHA: "deadbeef", ShortSHA: "deadbeef"},
			Source: "push", DefaultImage: "alpine:3.24",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Status != "success" {
		t.Fatalf("bridge=%+v", res)
	}
	if _, err := os.Stat(filepath.Join(dir, ".glci", "child-bridge", "logs", "kid.log")); err != nil {
		t.Fatalf("child job did not run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".glci", "cache", "keep", "x")); err != nil {
		t.Fatal("parent cache was wiped by a child pipeline")
	}
}

func TestParallelDotenvNeed(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".gitlab-ci.yml", `
unit:
  parallel: 2
  script: echo FOO=bar > a.env
  artifacts:
    reports:
      dotenv: a.env
down:
  needs: [unit]
  script: test "$FOO" = "bar"
`)
	p := mustCompile(t, dir)
	if _, err := Run(Options{Root: dir, Pipeline: p, Jobs: p.Jobs, Executor: "shell", Stdout: os.Stdout, Stderr: os.Stderr}); err != nil {
		t.Fatal(err)
	}
}

func mustCompile(t *testing.T, dir string) *gitlabci.Pipeline {
	t.Helper()
	p, err := gitlabci.Compile(gitlabci.CompileOptions{
		Root: dir, File: ".gitlab-ci.yml",
		Git:    gitctx.Info{Root: dir, Branch: "main", Ref: "main", DefaultBranch: "main", SHA: "deadbeef", ShortSHA: "deadbeef"},
		Source: "push", DefaultImage: "alpine:3.24",
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func names(jobs []gitlabci.Job) []string {
	var o []string
	for _, j := range jobs {
		o = append(o, j.Name)
	}
	return o
}
