package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ttopias/glci/internal/gitctx"
	"github.com/ttopias/glci/internal/gitlabci"
)

func skipDockerE2E(t *testing.T) {
	t.Helper()
	err := dockerPing()
	switch dockerE2EAction(err == nil, githubActionsEnv()) {
	case "run":
		return
	case "fatal":
		t.Fatalf("docker is required on GitHub Actions; docker info failed: %v", err)
	default:
		t.Skip("docker not available")
	}
}

func dockerE2EAction(available bool, githubActions string) string {
	if available {
		return "run"
	}
	if githubActions == "true" {
		return "fatal"
	}
	return "skip"
}

func githubActionsEnv() string {
	if v := os.Getenv("GITHUB_ACTIONS"); v != "" {
		return v
	}
	return os.Getenv("CI")
}

func TestDockerE2EAction(t *testing.T) {
	if dockerE2EAction(true, "true") != "run" || dockerE2EAction(true, "") != "run" {
		t.Fatal("available docker must run e2e")
	}
	if dockerE2EAction(false, "true") != "fatal" {
		t.Fatal("GitHub Actions must fail closed without docker")
	}
	if dockerE2EAction(false, "") != "skip" {
		t.Fatal("local runs skip when docker info fails")
	}
}

func TestApplyHostDockerVars(t *testing.T) {
	m := map[string]string{
		"DOCKER_HOST":        "tcp://docker:2375",
		"DOCKER_TLS_CERTDIR": "/certs",
		"DOCKER_CERT_PATH":   "/certs/client",
		"DOCKER_TLS_VERIFY":  "1",
	}
	applyHostDockerVars(m)
	if m["DOCKER_HOST"] != "unix:///var/run/docker.sock" {
		t.Fatalf("DOCKER_HOST=%q", m["DOCKER_HOST"])
	}
	if v, ok := m["DOCKER_TLS_CERTDIR"]; !ok || v != "" {
		t.Fatalf("DOCKER_TLS_CERTDIR ok=%v v=%q", ok, v)
	}
	env := envList(m)
	foundEmptyTLS := false
	for _, e := range env {
		if e == "DOCKER_TLS_CERTDIR=" {
			foundEmptyTLS = true
		}
		if strings.HasPrefix(e, "DOCKER_CERT_PATH=") || strings.HasPrefix(e, "DOCKER_TLS_VERIFY=") {
			t.Fatalf("tls leftover %q", e)
		}
	}
	if !foundEmptyTLS {
		t.Fatalf("empty DOCKER_TLS_CERTDIR missing from docker -e list: %v", env)
	}

	applyHostDockerVars(nil)
}

func TestIsDindImage(t *testing.T) {
	if !isDindImage("docker:29-dind") || !isDindImage("docker:dind") {
		t.Fatal("expected dind images to match")
	}
	if isDindImage("nginx:alpine") || isDindImage("docker:29") {
		t.Fatal("non-dind images must still start as services")
	}
}

func TestJobNeedsNetwork(t *testing.T) {
	if jobNeedsNetwork(gitlabci.Job{}) {
		t.Fatal("no services should use the default bridge")
	}
	if jobNeedsNetwork(gitlabci.Job{Services: []gitlabci.Service{{Name: "docker:29-dind"}}}) {
		t.Fatal("skipped dind sidecars should not create a job network")
	}
	if !jobNeedsNetwork(gitlabci.Job{Services: []gitlabci.Service{{Name: "nginx:alpine"}}}) {
		t.Fatal("real services need a user-defined network")
	}
}

func TestUnixSocketFile(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "docker.sock")
	if err := os.WriteFile(sock, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := unixSocketFile("unix://" + sock); got != sock {
		t.Fatalf("got %q want %q", got, sock)
	}
	if got := unixSocketFile("  UNIX://" + sock + "  "); got != sock {
		t.Fatalf("case/pad got %q", got)
	}
	if unixSocketFile("unix://relative/docker.sock") != "" {
		t.Fatal("relative unix path must be rejected")
	}
	raw := dir + "/../" + filepath.Base(dir) + "/docker.sock"
	if got := unixSocketFile("unix://" + raw); got != sock {
		t.Fatalf("cleaned abs path got %q want %q", got, sock)
	}
	if unixSocketFile("tcp://docker:2375") != "" {
		t.Fatal("tcp host is not a unix socket")
	}
	if unixSocketFile("unix://"+dir) != "" {
		t.Fatal("directory must not be treated as a socket")
	}
}

func TestPickDockerSocket(t *testing.T) {
	dir := t.TempDir()
	writeFile := func(name string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	varRun := writeFile("docker.sock")
	envSock := writeFile("env.sock")
	ctxSock := writeFile("ctx.sock")
	fallback := writeFile("fallback.sock")
	if got := pickDockerSocket("unix://"+envSock, "unix://"+ctxSock, []string{varRun, fallback}); got != varRun {
		t.Fatalf("/var/run-style socket must win for VM bind-mounts, got %q", got)
	}
	if got := pickDockerSocket("unix://"+envSock, "unix://"+ctxSock, []string{filepath.Join(dir, "missing.sock"), fallback}); got != envSock {
		t.Fatalf("env host should win when /var/run is absent, got %q", got)
	}
	if got := pickDockerSocket("", "unix://"+ctxSock, []string{filepath.Join(dir, "missing.sock"), fallback}); got != ctxSock {
		t.Fatalf("docker context should beat later candidates, got %q", got)
	}
	if got := pickDockerSocket("", "tcp://127.0.0.1:2375", []string{filepath.Join(dir, "missing.sock"), fallback}); got != fallback {
		t.Fatalf("candidates after non-unix context, got %q", got)
	}
	if pickDockerSocket("", "", []string{filepath.Join(dir, "missing.sock")}) != "" {
		t.Fatal("expected no socket")
	}
}

func TestDockerPipeline(t *testing.T) {
	skipDockerE2E(t)
	dir := t.TempDir()
	write(t, dir, "ci/common.yml", `
.setup:
  before_script:
    - echo setup-from-include
`)
	write(t, dir, ".gitlab-ci.yml", `
include:
  - local: ci/common.yml

default:
  image: alpine:3.24

variables:
  GREETING: hello

.hidden:
  script: echo hidden

build:
  extends: .setup
  stage: build
  hooks:
    pre_get_sources_script:
      - echo hook-ok
  script:
    - !reference [.setup, before_script]
    - echo "$GREETING" > artifact.txt
    - echo "VERSION=9" > build.env
    - echo "coverage: 42.0%"
  artifacts:
    paths: [artifact.txt]
    reports:
      dotenv: build.env
  coverage: '/coverage: (\d+\.\d+%)/'

test:
  stage: test
  needs: [build]
  image:
    name: alpine:3.24
    entrypoint: [""]
  script:
    - test -f artifact.txt
    - grep hello artifact.txt
    - test "$VERSION" = "9"
    - echo test-ok
`)
	p, err := gitlabci.Compile(gitlabci.CompileOptions{
		Root: dir, File: ".gitlab-ci.yml",
		Git:          gitctx.Info{Root: dir, Branch: "main", Ref: "main", DefaultBranch: "main", SHA: "deadbeef", ShortSHA: "deadbeef"},
		Source:       "push",
		DefaultImage: "alpine:3.24",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Jobs) != 2 {
		t.Fatalf("jobs=%d", len(p.Jobs))
	}
	res, err := Run(Options{
		Root: dir, Pipeline: p, Jobs: p.Jobs, Executor: "docker", Concurrency: 2,
		Stdout: os.Stdout, Stderr: os.Stderr, DefaultImage: "alpine:3.24",
	})
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]Result{}
	for _, r := range res {
		by[r.Name] = r
		if r.Status != "success" {
			t.Fatalf("%s: %s", r.Name, r.Status)
		}
	}
	if by["build"].Coverage != "42.0%" {
		t.Fatalf("coverage=%q", by["build"].Coverage)
	}
}

func TestDockerCustomImageAndService(t *testing.T) {
	skipDockerE2E(t)
	dir := t.TempDir()
	write(t, dir, ".gitlab-ci.yml", `
job:
  image: python:3.14-alpine
  services:
    - name: nginx:alpine
      alias: nginx
  before_script:
    - python --version
    - sleep 2
  script:
    - python -c "print('py-ok')"
    - python -c "import urllib.request; print(urllib.request.urlopen('http://nginx/').status)"
`)
	requireJobOK(t, runDockerCI(t, dir, compileCI(t, dir)), 1)
}

func TestDockerInDocker(t *testing.T) {
	skipDockerE2E(t)
	dir := t.TempDir()
	write(t, dir, ".gitlab-ci.yml", `
dind:
  image: docker:29
  script:
    - docker info
    - docker run --rm alpine:3.24 echo dind-ok
`)
	requireJobOK(t, runDockerCI(t, dir, compileCI(t, dir)), 1)
	if _, err := os.Stat(filepath.Join(dir, ".glci", "logs", "dind.log")); err != nil {
		t.Fatal(err)
	}
}

func TestDockerFileVarsAndChildPipeline(t *testing.T) {
	skipDockerE2E(t)
	dir := t.TempDir()
	write(t, dir, "child.yml", `
kid:
  image: alpine:3.24
  script:
    - echo child-ok
`)
	write(t, dir, ".gitlab-ci.yml", `
filejob:
  image: alpine:3.24
  variables:
    KEYFILE:
      value: secret-contents
      file: true
  script:
    - grep secret-contents "$KEYFILE"
bridge:
  trigger:
    include: child.yml
    strategy: depend
`)
	p, err := gitlabci.Compile(gitlabci.CompileOptions{
		Root: dir, File: ".gitlab-ci.yml",
		Git:    gitctx.Info{Root: dir, Branch: "main", Ref: "main", DefaultBranch: "main"},
		Source: "push", DefaultImage: "alpine:3.24",
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := Run(Options{
		Root: dir, Pipeline: p, Jobs: p.Jobs, Executor: "docker",
		Stdout: os.Stdout, Stderr: os.Stderr, DefaultImage: "alpine:3.24",
		Compile: gitlabci.CompileOptions{
			Root: dir, Git: gitctx.Info{Root: dir, Branch: "main", Ref: "main", DefaultBranch: "main"},
			Source: "push", DefaultImage: "alpine:3.24",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]Result{}
	for _, r := range res {
		by[r.Name] = r
		if r.Status != "success" {
			t.Fatalf("%s: %s", r.Name, r.Status)
		}
	}
	if _, ok := by["filejob"]; !ok {
		t.Fatalf("missing filejob %+v", res)
	}
}

func TestDockerCache(t *testing.T) {
	skipDockerE2E(t)
	dir := t.TempDir()
	write(t, dir, ".gitlab-ci.yml", `
one:
  image: alpine:3.24
  cache:
    key: k1
    paths: [cached]
  script:
    - mkdir -p cached
    - echo 1 > cached/a
two:
  image: alpine:3.24
  needs: [one]
  cache:
    key: k1
    paths: [cached]
  script:
    - test -f cached/a
`)
	requireJobOK(t, runDockerCI(t, dir, compileCI(t, dir)), 2)
}

func TestDockerHostSocketBuild(t *testing.T) {
	skipDockerE2E(t)
	if dockerSocketPath() == "" {
		t.Fatal("docker is available but no unix socket was found to bind-mount")
	}
	dir := t.TempDir()
	write(t, dir, ".gitlab-ci.yml", `
buildimg:
  image: docker:29
  script:
    - docker info
    - printf 'FROM alpine:3.24\nCMD echo built-ok\n' > Dockerfile
    - docker build -t glci-e2e-sock:local .
    - docker run --rm glci-e2e-sock:local
    - docker rmi glci-e2e-sock:local
`)
	requireJobOK(t, runDockerCI(t, dir, compileCI(t, dir)), 1)
}

func TestDockerDindServiceWithoutDockerHost(t *testing.T) {
	skipDockerE2E(t)
	dir := t.TempDir()
	write(t, dir, ".gitlab-ci.yml", `
job:
  image: docker:29
  services:
    - name: docker:29-dind
      alias: docker
    - name: nginx:alpine
      alias: nginx
  variables:
    DOCKER_TLS_CERTDIR: "/certs"
    DOCKER_HOST: tcp://docker:2375
  script:
    - docker info
    - docker run --rm alpine:3.24 echo host-sock-ok
    - wget -qO- http://nginx/ >/dev/null
`)
	p := compileCI(t, dir)
	requireJobOK(t, runDockerCI(t, dir, p), 1)
}

func compileCI(t *testing.T, dir string) *gitlabci.Pipeline {
	t.Helper()
	p, err := gitlabci.Compile(gitlabci.CompileOptions{
		Root: dir, File: ".gitlab-ci.yml",
		Git:    gitctx.Info{Root: dir, Branch: "main", Ref: "main", DefaultBranch: "main"},
		Source: "push", DefaultImage: "alpine:3.24",
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func runDockerCI(t *testing.T, dir string, p *gitlabci.Pipeline) []Result {
	t.Helper()
	res, err := Run(Options{
		Root: dir, Pipeline: p, Jobs: p.Jobs, Executor: "docker",
		Stdout: os.Stdout, Stderr: os.Stderr,
	})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func requireJobOK(t *testing.T, res []Result, n int) {
	t.Helper()
	if len(res) != n {
		t.Fatalf("res=%+v", res)
	}
	for _, r := range res {
		if r.Status != "success" {
			t.Fatalf("%s: %s", r.Name, r.Status)
		}
	}
}
