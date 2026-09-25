package runner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ttopias/glci/internal/gitlabci"
)

func TestNeedsArtifactsFalseDoesNotRestore(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".gitlab-ci.yml", `
stages: [build, test]
producer:
  stage: build
  script:
    - echo secret > artifact.txt
  artifacts:
    paths: [artifact.txt]
consumer:
  stage: test
  needs:
    - job: producer
      artifacts: false
  script:
    - test ! -f artifact.txt
`)
	p := mustCompile(t, dir)
	res, err := Run(Options{Root: dir, Pipeline: p, Jobs: p.Jobs, Executor: "shell", Stdout: os.Stdout, Stderr: os.Stderr})
	if err != nil {
		t.Fatal(err)
	}
	assertAllSuccess(t, res)
}

func TestNeedsDefaultArtifactsTrue(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".gitlab-ci.yml", `
stages: [build, test]
producer:
  stage: build
  script:
    - echo from-needs > artifact.txt
  artifacts:
    paths: [artifact.txt]
consumer:
  stage: test
  needs: [producer]
  script:
    - test -f artifact.txt
    - grep from-needs artifact.txt
`)
	p := mustCompile(t, dir)
	res, err := Run(Options{Root: dir, Pipeline: p, Jobs: p.Jobs, Executor: "shell", Stdout: os.Stdout, Stderr: os.Stderr})
	if err != nil {
		t.Fatal(err)
	}
	assertAllSuccess(t, res)
}

func TestDependenciesEmptyRestoresNothing(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".gitlab-ci.yml", `
stages: [build, test]
producer:
  stage: build
  script:
    - echo should-not-see > artifact.txt
  artifacts:
    paths: [artifact.txt]
consumer:
  stage: test
  dependencies: []
  script:
    - test ! -f artifact.txt
`)
	p := mustCompile(t, dir)
	for _, j := range p.Jobs {
		if j.Name == "consumer" {
			if j.Dependencies == nil {
				t.Fatal("dependencies: [] must compile to non-nil empty slice")
			}
			if len(j.Dependencies) != 0 {
				t.Fatalf("dependencies=%v", j.Dependencies)
			}
		}
	}
	res, err := Run(Options{Root: dir, Pipeline: p, Jobs: p.Jobs, Executor: "shell", Stdout: os.Stdout, Stderr: os.Stderr})
	if err != nil {
		t.Fatal(err)
	}
	assertAllSuccess(t, res)
}

func TestDependenciesListsSpecificJobs(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".gitlab-ci.yml", `
stages: [build, test]
keep:
  stage: build
  script:
    - echo keep-me > keep.txt
  artifacts:
    paths: [keep.txt]
drop:
  stage: build
  script:
    - echo drop-me > drop.txt
  artifacts:
    paths: [drop.txt]
consumer:
  stage: test
  dependencies: [keep]
  script:
    - test -f keep.txt
    - test ! -f drop.txt
`)
	p := mustCompile(t, dir)
	res, err := Run(Options{Root: dir, Pipeline: p, Jobs: p.Jobs, Executor: "shell", Stdout: os.Stdout, Stderr: os.Stderr})
	if err != nil {
		t.Fatal(err)
	}
	assertAllSuccess(t, res)
}

func TestNeedsWithEmptyDependenciesRestoresNothing(t *testing.T) {
	// GitLab: dependencies: [] wins over needs for artifact download.
	dir := t.TempDir()
	write(t, dir, ".gitlab-ci.yml", `
stages: [build, test]
via_needs:
  stage: build
  script:
    - echo from-needs > needs.txt
  artifacts:
    paths: [needs.txt]
consumer:
  stage: test
  needs:
    - job: via_needs
      artifacts: true
  dependencies: []
  script:
    - test ! -f needs.txt
`)
	p := mustCompile(t, dir)
	res, err := Run(Options{Root: dir, Pipeline: p, Jobs: p.Jobs, Executor: "shell", Stdout: os.Stdout, Stderr: os.Stderr})
	if err != nil {
		t.Fatal(err)
	}
	assertAllSuccess(t, res)
}

func TestNeedsDependenciesIntersection(t *testing.T) {
	// Non-empty dependencies intersects with needs artifacts:true candidates.
	dir := t.TempDir()
	write(t, dir, ".gitlab-ci.yml", `
stages: [build, test]
keep:
  stage: build
  script:
    - echo keep-me > keep.txt
  artifacts:
    paths: [keep.txt]
drop:
  stage: build
  script:
    - echo drop-me > drop.txt
  artifacts:
    paths: [drop.txt]
consumer:
  stage: test
  needs: [keep, drop]
  dependencies: [keep]
  script:
    - test -f keep.txt
    - test ! -f drop.txt
`)
	p := mustCompile(t, dir)
	res, err := Run(Options{Root: dir, Pipeline: p, Jobs: p.Jobs, Executor: "shell", Stdout: os.Stdout, Stderr: os.Stderr})
	if err != nil {
		t.Fatal(err)
	}
	assertAllSuccess(t, res)
}

func TestDotenvFollowsArtifactSelection(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".gitlab-ci.yml", `
stages: [build, test]
keep:
  stage: build
  script:
    - echo KEEP=yes > keep.env
  artifacts:
    reports:
      dotenv: keep.env
drop:
  stage: build
  script:
    - echo DROP=yes > drop.env
  artifacts:
    reports:
      dotenv: drop.env
consumer:
  stage: test
  needs: [keep, drop]
  dependencies: [keep]
  script:
    - test "$KEEP" = "yes"
    - test -z "${DROP:-}"
`)
	p := mustCompile(t, dir)
	res, err := Run(Options{Root: dir, Pipeline: p, Jobs: p.Jobs, Executor: "shell", Stdout: os.Stdout, Stderr: os.Stderr})
	if err != nil {
		t.Fatal(err)
	}
	assertAllSuccess(t, res)
}

func TestDotenvEmptyWithNeedsAndEmptyDependencies(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".gitlab-ci.yml", `
stages: [build, test]
producer:
  stage: build
  script:
    - echo FOO=from-dotenv > build.env
  artifacts:
    reports:
      dotenv: build.env
consumer:
  stage: test
  needs: [producer]
  dependencies: []
  script:
    - test -z "${FOO:-}"
`)
	p := mustCompile(t, dir)
	res, err := Run(Options{Root: dir, Pipeline: p, Jobs: p.Jobs, Executor: "shell", Stdout: os.Stdout, Stderr: os.Stderr})
	if err != nil {
		t.Fatal(err)
	}
	assertAllSuccess(t, res)
}

func TestDefaultPreviousStageArtifacts(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".gitlab-ci.yml", `
stages: [build, test]
producer:
  stage: build
  script:
    - echo prev-stage > artifact.txt
  artifacts:
    paths: [artifact.txt]
consumer:
  stage: test
  script:
    - test -f artifact.txt
`)
	p := mustCompile(t, dir)
	res, err := Run(Options{Root: dir, Pipeline: p, Jobs: p.Jobs, Executor: "shell", Stdout: os.Stdout, Stderr: os.Stderr})
	if err != nil {
		t.Fatal(err)
	}
	assertAllSuccess(t, res)
}

func TestArtifactSourcesSelection(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".gitlab-ci.yml", `
stages: [build, test]
a:
  stage: build
  script: echo a
b:
  stage: build
  script: echo b
with_needs:
  stage: test
  needs:
    - job: a
      artifacts: false
    - job: b
  script: echo x
empty_deps:
  stage: test
  dependencies: []
  script: echo x
listed_deps:
  stage: test
  dependencies: [a]
  script: echo x
needs_empty_deps:
  stage: test
  needs: [a, b]
  dependencies: []
  script: echo x
needs_intersect:
  stage: test
  needs: [a, b]
  dependencies: [a]
  script: echo x
plain:
  stage: test
  script: echo x
`)
	p := mustCompile(t, dir)
	byName := map[string]gitlabci.Job{}
	for _, j := range p.Jobs {
		byName[j.Name] = j
	}
	stages := p.Stages

	got := artifactSources(byName["with_needs"], p.Jobs, stages)
	if len(got) != 1 || got[0] != "b" {
		t.Fatalf("needs artifacts filter: %v", got)
	}
	got = artifactSources(byName["empty_deps"], p.Jobs, stages)
	if got == nil || len(got) != 0 {
		t.Fatalf("empty deps: %v (nil=%v)", got, got == nil)
	}
	got = artifactSources(byName["listed_deps"], p.Jobs, stages)
	if len(got) != 1 || got[0] != "a" {
		t.Fatalf("listed deps: %v", got)
	}
	got = artifactSources(byName["needs_empty_deps"], p.Jobs, stages)
	if got == nil || len(got) != 0 {
		t.Fatalf("needs + empty deps: %v", got)
	}
	got = artifactSources(byName["needs_intersect"], p.Jobs, stages)
	if len(got) != 1 || got[0] != "a" {
		t.Fatalf("needs ∩ deps: %v", got)
	}
	got = artifactSources(byName["plain"], p.Jobs, stages)
	sortNames := map[string]bool{}
	for _, n := range got {
		sortNames[n] = true
	}
	if !sortNames["a"] || !sortNames["b"] || len(got) != 2 {
		t.Fatalf("default previous stages: %v", got)
	}
}

func TestDebugDumpDistinguishesEmptyDependencies(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".gitlab-ci.yml", `
stages: [test]
omitted:
  stage: test
  script: echo omitted
empty_deps:
  stage: test
  dependencies: []
  script: echo empty
`)
	p := mustCompile(t, dir)
	opts := Options{Root: dir, Pipeline: p, Jobs: p.Jobs, Executor: "shell", Debug: true, Stdout: os.Stdout, Stderr: os.Stderr}
	if _, err := Run(opts); err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(dir, ".glci")

	checkMode := func(path string) {
		t.Helper()
		st, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm()&0o077 != 0 {
			t.Fatalf("%s mode=%o, want 0600", path, st.Mode().Perm())
		}
	}

	omittedJSON := filepath.Join(work, "builds", "omitted", ".glci", "job.json")
	emptyJSON := filepath.Join(work, "builds", "empty_deps", ".glci", "job.json")
	checkMode(omittedJSON)
	checkMode(emptyJSON)
	checkMode(filepath.Join(work, "builds", "omitted", ".glci", "variables.env"))

	omitted := readJobDebugJSON(t, omittedJSON)
	empty := readJobDebugJSON(t, emptyJSON)
	if omitted["dependencies"] != nil {
		t.Fatalf("omitted dependencies want null, got %#v", omitted["dependencies"])
	}
	deps, ok := empty["dependencies"].([]any)
	if !ok || len(deps) != 0 {
		t.Fatalf("empty dependencies want [], got %#v", empty["dependencies"])
	}
}

func readJobDebugJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("%s: %v\n%s", path, err, b)
	}
	return v
}

func assertAllSuccess(t *testing.T, res []Result) {
	t.Helper()
	for _, r := range res {
		if r.Status != "success" {
			t.Fatalf("%s: status=%s log=%s", r.Name, r.Status, r.LogPath)
		}
	}
}
