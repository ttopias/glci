package gitlabci

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestInstallShSyntax(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	script := filepath.Join(filepath.Dir(file), "..", "..", "install.sh")
	cmd := exec.Command("sh", "-n", script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("install.sh: %v\n%s", err, out)
	}
}
