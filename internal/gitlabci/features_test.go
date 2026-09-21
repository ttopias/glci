package gitlabci

import (
	"strings"
	"testing"

	"github.com/ttopias/glci/internal/gitctx"
)

func TestYAMLAnchors(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".gitlab-ci.yml", `
.foo: &foo
  script:
    - echo anchored
job:
  <<: *foo
  stage: test
`)
	p := compileDir(t, dir, "main")
	if len(p.Jobs) != 1 || !strings.Contains(strings.Join(p.Jobs[0].Script, "\n"), "anchored") {
		t.Fatalf("jobs=%+v", p.Jobs)
	}
}

func TestInheritAndOnlyExcept(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".gitlab-ci.yml", `
default:
  image: alpine:3.24
  retry: 2
variables:
  KEEP: yes
  DROP: no
keep:
  inherit:
    default: [image]
    variables: [KEEP]
  script: echo $KEEP
  only:
    - main
drop:
  script: echo x
  except:
    - main
`)
	p := compileDir(t, dir, "main")
	if len(p.Jobs) != 1 || p.Jobs[0].Name != "keep" {
		t.Fatalf("jobs=%v", names(p.Jobs))
	}
	if p.Jobs[0].Image == nil || p.Jobs[0].Image.Name != "alpine:3.24" {
		t.Fatalf("image=%+v", p.Jobs[0].Image)
	}
	if p.Jobs[0].Retry != nil {
		t.Fatalf("retry should not inherit: %+v", p.Jobs[0].Retry)
	}
	if p.Jobs[0].Variables["KEEP"] != "yes" {
		t.Fatalf("KEEP=%q", p.Jobs[0].Variables["KEEP"])
	}
}

func TestFileVariablesSecretsPages(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".gitlab-ci.yml", `
job:
  script: cat "$KEYFILE"
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
  pages:
    publish: public
  environment:
    name: review/$CI_COMMIT_REF_SLUG
    url: http://example.local
  timeout: 1 hour
  retry:
    max: 2
    when: [script_failure]
  after_script:
    - echo done
`)
	p := compileDir(t, dir, "main")
	if len(p.Jobs) != 1 {
		t.Fatalf("jobs=%v", names(p.Jobs))
	}
	j := p.Jobs[0]
	if j.FileVariables["KEYFILE"] != "secret-contents" {
		t.Fatalf("file vars=%v", j.FileVariables)
	}
	if len(j.Secrets) == 0 || len(j.IDTokens) == 0 {
		t.Fatalf("secrets=%v tokens=%v", j.Secrets, j.IDTokens)
	}
	if j.Publish != "public" || j.Environment == nil || j.Timeout == 0 || j.Retry == nil {
		t.Fatalf("pages/env/timeout/retry: %+v", j)
	}
	if len(j.AfterScript) != 1 {
		t.Fatalf("after_script=%v", j.AfterScript)
	}
}

func TestIncludeProjectTemplateComponent(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "other/.gitlab-ci.yml", "from_project:\n  script: echo project\n")
	write(t, dir, "templates/Jobs/Test.gitlab-ci.yml", "from_template:\n  script: echo template\n")
	write(t, dir, "comp/template.yml", "spec:\n  inputs:\n    n:\n      default: n1\n---\nfrom_component:\n  script: echo $[[ inputs.n ]]\n")
	write(t, dir, ".gitlab-ci.yml", `
include:
  - project: group/other
    file: /.gitlab-ci.yml
  - template: Jobs/Test.gitlab-ci.yml
  - component: gitlab.com/org/comp@1.0
`)
	p, err := Compile(CompileOptions{
		Root: dir, File: ".gitlab-ci.yml",
		Git:          gitctx.Info{Root: dir, Branch: "main", Ref: "main", DefaultBranch: "main", SHA: "abc12345", ShortSHA: "abc12345"},
		Source:       "push",
		DefaultImage: "alpine:3.24",
		Projects:     map[string]string{"group/other": "other"},
		TemplatesDir: dir + "/templates",
		Components:   map[string]string{"gitlab.com/org/comp": "comp"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, j := range p.Jobs {
		got[j.Name] = true
	}
	for _, name := range []string{"from_project", "from_template", "from_component"} {
		if !got[name] {
			t.Fatalf("missing %s in %v", name, names(p.Jobs))
		}
	}
}

func TestChildPipelineAndCoverage(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "child.yml", "child:\n  script: echo child\n")
	write(t, dir, ".gitlab-ci.yml", `
parent:
  script: echo cover 12.5%
  coverage: '/cover (\d+\.\d+%)/'
  artifacts:
    paths: [out]
    reports:
      dotenv: a.env
bridge:
  trigger:
    include: child.yml
    strategy: depend
`)
	p := compileDir(t, dir, "main")
	if len(p.Jobs) != 2 {
		t.Fatalf("jobs=%v", names(p.Jobs))
	}
	var parent, bridge Job
	for _, j := range p.Jobs {
		switch j.Name {
		case "parent":
			parent = j
		case "bridge":
			bridge = j
		}
	}
	if parent.Coverage == "" || parent.Artifacts == nil || len(parent.Artifacts.Dotenv) == 0 {
		t.Fatalf("parent=%+v", parent)
	}
	if bridge.Trigger == nil || bridge.Trigger.Strategy != "depend" {
		t.Fatalf("bridge=%+v", bridge)
	}
}

func TestServicesAndCacheCompile(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "go.mod", "module x\n")
	write(t, dir, ".gitlab-ci.yml", `
job:
  image: docker:29
  services:
    - name: docker:29-dind
      alias: docker
  variables:
    DOCKER_TLS_CERTDIR: ""
  cache:
    key:
      files: [go.mod]
      prefix: mods
    paths: [.cache]
  script:
    - docker info
`)
	p := compileDir(t, dir, "main")
	if len(p.Jobs) != 1 {
		t.Fatalf("jobs=%v", names(p.Jobs))
	}
	j := p.Jobs[0]
	if len(j.Services) != 1 || j.Services[0].Alias != "docker" {
		t.Fatalf("services=%+v", j.Services)
	}
	if len(j.Cache) != 1 || j.Cache[0].KeyPrefix != "mods" {
		t.Fatalf("cache=%+v", j.Cache)
	}
}
