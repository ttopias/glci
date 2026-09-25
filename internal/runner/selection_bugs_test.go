package runner

import (
	"os"
	"testing"

	"github.com/ttopias/glci/internal/gitlabci"
)

// --job NAME should run that manual even when FilterJobs pulls in other
// unselected when:manual jobs from earlier stages (no needs: []).
func TestSelectedManualNotBlockedByUnselectedManualSkip(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".gitlab-ci.yml", `
stages: [build, deploy]
auto:
  stage: build
  script: echo auto
other_manual:
  stage: build
  when: manual
  script: echo other
selected_manual:
  stage: deploy
  when: manual
  script: echo selected
`)
	p := mustCompile(t, dir)
	jobs := gitlabci.FilterJobs(p, []string{"selected_manual"})
	res, err := Run(Options{
		Root: dir, Pipeline: p, Jobs: jobs, Executor: "shell",
		SelectedJobs: []string{"selected_manual"},
		Stdout:       os.Stdout, Stderr: os.Stderr,
	})
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]Result{}
	for _, r := range res {
		by[r.Name] = r
	}
	if by["selected_manual"].Status != "success" || by["selected_manual"].Skipped {
		t.Fatalf("selected manual should run via --job, got %+v (all=%+v)", by["selected_manual"], by)
	}
	if !by["other_manual"].Skipped {
		t.Fatalf("unselected manual should stay skipped, got %+v", by["other_manual"])
	}
}

// --job NAME --manual must not run every when:manual job that FilterJobs
// pulled in as a stage predecessor of the selected job.
func TestSelectedJobWithIncludeManualDoesNotRunOtherManuals(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".gitlab-ci.yml", `
stages: [build, deploy]
build_a:
  stage: build
  script: echo a
manual_a:
  stage: build
  when: manual
  script: echo ma
manual_b:
  stage: deploy
  when: manual
  script: echo mb
`)
	p := mustCompile(t, dir)
	jobs := gitlabci.FilterJobs(p, []string{"manual_b"})
	res, err := Run(Options{
		Root: dir, Pipeline: p, Jobs: jobs, Executor: "shell",
		IncludeManual: true, SelectedJobs: []string{"manual_b"},
		Stdout: os.Stdout, Stderr: os.Stderr,
	})
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]Result{}
	for _, r := range res {
		by[r.Name] = r
	}
	if by["manual_b"].Status != "success" || by["manual_b"].Skipped {
		t.Fatalf("selected manual_b should run, got %+v", by["manual_b"])
	}
	if by["manual_a"].Status == "success" && !by["manual_a"].Skipped {
		t.Fatalf("manual_a must not run for --job manual_b --manual, got %+v", by)
	}
}

// Parallel needs chains plus stage-ordered jobs: a skipped upstream (unplayed
// manual) must not skip unrelated needs chains or later jobs that do not need it.
func TestNeedsDAGIndependentChainsNotBlockedBySkippedUpstream(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".gitlab-ci.yml", `
stages: [build, mid, final]
job1:
  stage: build
  when: manual
  script: echo 1
job2:
  stage: build
  script: echo 2
job3:
  stage: build
  script: echo 3
job9:
  stage: mid
  needs: [job1]
  script: echo 9
job10:
  stage: mid
  needs: [job2]
  script: echo 10
job11:
  stage: mid
  needs: [job3]
  script: echo 11
job22:
  stage: final
  needs: [job9]
  script: echo 22
job23:
  stage: final
  needs: [job10]
  script: echo 23
job24:
  stage: final
  needs: [job11]
  script: echo 24
later_no_needs:
  stage: final
  script: echo later
`)
	p := mustCompile(t, dir)
	res, err := Run(Options{
		Root: dir, Pipeline: p, Jobs: p.Jobs, Executor: "shell", Concurrency: 4,
		Stdout: os.Stdout, Stderr: os.Stderr,
	})
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]Result{}
	for _, r := range res {
		by[r.Name] = r
	}
	if !by["job1"].Skipped || !by["job9"].Skipped || !by["job22"].Skipped {
		t.Fatalf("manual job1 chain should skip, got job1=%+v job9=%+v job22=%+v", by["job1"], by["job9"], by["job22"])
	}
	for _, name := range []string{"job2", "job3", "job10", "job11", "job23", "job24"} {
		if by[name].Status != "success" || by[name].Skipped {
			t.Fatalf("%s should succeed on an independent needs chain, got %+v", name, by[name])
		}
	}
	if by["later_no_needs"].Status != "success" || by["later_no_needs"].Skipped {
		t.Fatalf("later_no_needs should run; skipped manual must not block the stage, got %+v", by["later_no_needs"])
	}
}

// --job downstream must still run a needed when:manual gate (real needs),
// while leaving unrelated stage-sibling manuals skipped.
func TestSelectedJobRunsNeededManual(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".gitlab-ci.yml", `
stages: [build, test]
sibling_manual:
  stage: build
  when: manual
  script: echo sibling
gate:
  stage: build
  when: manual
  script: echo gate
downstream:
  stage: test
  needs: [gate]
  script: echo down
`)
	p := mustCompile(t, dir)
	jobs := gitlabci.FilterJobs(p, []string{"downstream"})
	res, err := Run(Options{
		Root: dir, Pipeline: p, Jobs: jobs, Executor: "shell",
		SelectedJobs: []string{"downstream"},
		Stdout:       os.Stdout, Stderr: os.Stderr,
	})
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]Result{}
	for _, r := range res {
		by[r.Name] = r
	}
	if by["gate"].Status != "success" || by["gate"].Skipped {
		t.Fatalf("needed manual gate should run with --job downstream, got %+v", by["gate"])
	}
	if by["downstream"].Status != "success" || by["downstream"].Skipped {
		t.Fatalf("downstream should succeed, got %+v", by["downstream"])
	}
	if s, ok := by["sibling_manual"]; ok && !s.Skipped {
		t.Fatalf("sibling_manual must stay skipped, got %+v", s)
	}
}
