package gitlabci

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadLocalConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := LoadLocalConfig(dir)
	if cfg.Manual != "" || cfg.AllowRemote {
		t.Fatalf("empty cfg=%+v", cfg)
	}
	write(t, dir, ".glci.yml", `
variables:
  TOKEN: abc
projects:
  group/other: ../other
components:
  gitlab.com/org/comp: ./comp
templates_dir: ./templates
manual: run
executor: docker
privileged: true
protected_branches: [main, release]
allow_remote: true
inputs:
  name: world
`)
	cfg = LoadLocalConfig(dir)
	if cfg.Manual != "run" || !cfg.AllowRemote || !cfg.Privileged || cfg.Executor != "docker" {
		t.Fatalf("cfg=%+v", cfg)
	}
	if cfg.Variables["TOKEN"] != "abc" || cfg.Projects["group/other"] != "../other" {
		t.Fatalf("maps=%+v", cfg)
	}
	if !IsProtected(cfg, "main") || IsProtected(cfg, "dev") {
		t.Fatalf("protected=%v", cfg.Protected)
	}
}

func TestLoadLocalConfigYamlAlias(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".glci.yaml"), []byte("manual: skip\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := LoadLocalConfig(dir)
	if cfg.Manual != "skip" {
		t.Fatalf("manual=%q", cfg.Manual)
	}
}
