package gitctx

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectNonGit(t *testing.T) {
	dir := t.TempDir()
	info, err := Detect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.DefaultBranch != "main" {
		t.Fatalf("default=%q", info.DefaultBranch)
	}
	if info.Root == "" {
		t.Fatal("root empty")
	}
}

func TestDetectGitRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=glci", "GIT_AUTHOR_EMAIL=glci@example.com",
			"GIT_COMMITTER_NAME=glci", "GIT_COMMITTER_EMAIL=glci@example.com",
			"GIT_TEMPLATE_DIR=", "GIT_CONFIG_NOSYSTEM=1",
			"HOME="+dir, "XDG_CONFIG_HOME="+dir)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "--template=", "-b", "main")
	run("config", "user.email", "glci@example.com")
	run("config", "user.name", "glci")
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "README")
	run("commit", "-m", "feat: first\n\nbody")
	info, err := Detect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Branch != "main" || info.Ref != "main" {
		t.Fatalf("branch=%q ref=%q", info.Branch, info.Ref)
	}
	if len(info.SHA) < 8 || info.ShortSHA != info.SHA[:8] {
		t.Fatalf("sha=%q short=%q", info.SHA, info.ShortSHA)
	}
	if !strings.Contains(info.Message, "feat: first") || info.Title != "feat: first" {
		t.Fatalf("msg=%q title=%q", info.Message, info.Title)
	}
	if !Exists(dir, "README") {
		t.Fatal("Exists README")
	}
	if Exists(dir, "missing") {
		t.Fatal("missing should not exist")
	}
}
