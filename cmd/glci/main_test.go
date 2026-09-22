package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestHelpAndVersion(t *testing.T) {
	out, err := capture(t, func() error { return run([]string{"help"}) })
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"glci run", "glci upgrade", "--manual", "--debug", "host Docker socket", "docker:*-dind"} {
		if !strings.Contains(out, s) {
			t.Fatalf("help missing %q:\n%s", s, out)
		}
	}
	out, err = capture(t, func() error { return run([]string{"version"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "glci version") {
		t.Fatalf("version=%q", out)
	}
}

func TestUnknownCommand(t *testing.T) {
	err := run([]string{"nope"})
	if err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("err=%v", err)
	}
}

func TestCLIListCompileRunManualAndJob(t *testing.T) {
	dir := t.TempDir()
	writeCI(t, dir, `
stages: [test, deploy]
auto:
  stage: test
  script: echo auto-ok
manual_job:
  stage: deploy
  when: manual
  script: echo manual-ok
  needs: []
`)
	out, err := capture(t, func() error { return run([]string{"list", "-C", dir}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "auto") || !strings.Contains(out, "manual_job") {
		t.Fatalf("list=%s", out)
	}
	if !strings.Contains(out, "source=push") {
		t.Fatalf("list source=%s", out)
	}

	out, err = capture(t, func() error { return run([]string{"compile", "-C", dir}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "manual_job") {
		t.Fatalf("compile=%s", out)
	}

	out, err = capture(t, func() error { return run([]string{"run", "-C", dir, "--shell"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "auto success") {
		t.Fatalf("run=%s", out)
	}
	if !strings.Contains(out, "skip manual_job") {
		t.Fatalf("expected skip manual, got %s", out)
	}

	out, err = capture(t, func() error { return run([]string{"run", "-C", dir, "--shell", "--manual"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "manual_job success") {
		t.Fatalf("manual run=%s", out)
	}

	out, err = capture(t, func() error {
		return run([]string{"run", "-C", dir, "--shell", "--job", "manual_job"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "manual_job success") {
		t.Fatalf("job run=%s", out)
	}
}

func TestCLIStageIncludesNeeds(t *testing.T) {
	dir := t.TempDir()
	writeCI(t, dir, `
stages: [build, test]
build:
  stage: build
  script: echo built > out.txt
  artifacts:
    paths: [out.txt]
test:
  stage: test
  needs: [build]
  script: test -f out.txt
`)
	out, err := capture(t, func() error { return run([]string{"run", "-C", dir, "--shell", "--stage", "test"}) })
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "build success") || !strings.Contains(out, "test success") {
		t.Fatalf("stage=%s", out)
	}
}

func TestCLIMRAndVarAndDryRun(t *testing.T) {
	dir := t.TempDir()
	writeCI(t, dir, `
job:
  script: echo "$EXTRA"
  rules:
    - if: $CI_PIPELINE_SOURCE == "merge_request_event"
`)
	out, err := capture(t, func() error { return run([]string{"list", "-C", dir}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "jobs=0") {
		t.Fatalf("push should drop MR-only job: %s", out)
	}
	out, err = capture(t, func() error { return run([]string{"list", "-C", dir, "--mr"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "merge_request_event") || !strings.Contains(out, "job") {
		t.Fatalf("mr list=%s", out)
	}

	writeCI(t, dir, "job:\n  script: echo hi\n")
	out, err = capture(t, func() error { return run([]string{"run", "-C", dir, "--shell", "--dry-run"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "dry-run") {
		t.Fatalf("dry-run=%s", out)
	}
}

func TestCLIExamplesBasicShell(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..")
	src := filepath.Join(root, "examples", "basic")
	dir := t.TempDir()
	data, err := os.ReadFile(filepath.Join(src, ".gitlab-ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitlab-ci.yml"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := capture(t, func() error { return run([]string{"run", "-C", dir, "--shell"}) })
	if err != nil {
		t.Fatalf("example basic: %v\n%s", err, out)
	}
	if !strings.Contains(out, "build success") {
		t.Fatalf("example=%s", out)
	}
	if strings.Contains(out, "manual_deploy success") {
		t.Fatalf("manual should not run: %s", out)
	}
}

func capture(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	oldOut, oldErr := os.Stdout, os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout, os.Stderr = w, w
	runErr := fn()
	w.Close()
	os.Stdout, os.Stderr = oldOut, oldErr
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	return buf.String(), runErr
}

func writeCI(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".gitlab-ci.yml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
