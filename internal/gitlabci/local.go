package gitlabci

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// LocalConfig is glci-only configuration (never sent to GitLab).
type LocalConfig struct {
	Variables    map[string]string `yaml:"variables"`
	Projects     map[string]string `yaml:"projects"`
	Components   map[string]string `yaml:"components"`
	TemplatesDir string            `yaml:"templates_dir"`
	Manual       string            `yaml:"manual"` // skip | run
	Executor     string            `yaml:"executor"`
	Privileged   bool              `yaml:"privileged"`
	Protected    []string          `yaml:"protected_branches"`
	AllowRemote  bool              `yaml:"allow_remote"`
	Inputs       map[string]string `yaml:"inputs"`
}

func LoadLocalConfig(root string) LocalConfig {
	cfg := LocalConfig{
		Variables:  map[string]string{},
		Projects:   map[string]string{},
		Components: map[string]string{},
	}
	for _, name := range []string{".glci.yml", ".glci.yaml", "glci.yml"} {
		p := filepath.Join(root, name)
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		_ = yaml.Unmarshal(b, &cfg)
		break
	}
	return cfg
}

func IsProtected(cfg LocalConfig, ref string) bool {
	for _, b := range cfg.Protected {
		if b == ref {
			return true
		}
	}
	return false
}
