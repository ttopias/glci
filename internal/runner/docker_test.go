package runner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ttopias/glci/internal/gitctx"
	"github.com/ttopias/glci/internal/gitlabci"
)

func TestDockerPipeline(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("docker not available")
	}
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
	if !dockerAvailable() {
		t.Skip("docker not available")
	}
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
		Stdout: os.Stdout, Stderr: os.Stderr,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Status != "success" {
		t.Fatalf("res=%+v", res)
	}
}

func TestDockerInDocker(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("docker not available")
	}
	if os.Getenv("GITHUB_ACTIONS") == "true" {
		t.Skip("nested docker-in-docker is not available on GitHub-hosted runners")
	}
	dir := t.TempDir()
	write(t, dir, ".gitlab-ci.yml", `
dind:
  image: docker:29
  services:
    - name: docker:29-dind
      alias: docker
  variables:
    DOCKER_TLS_CERTDIR: ""
    DOCKER_HOST: tcp://docker:2375
  script:
    - i=0; until docker info >/tmp/info.txt 2>/tmp/err.txt; do i=$((i+1)); if [ "$i" -gt 40 ]; then cat /tmp/err.txt; exit 1; fi; sleep 1; done
    - docker run --rm alpine:3.24 echo dind-ok
  artifacts:
    paths: [/tmp/info.txt]
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
		Stdout: os.Stdout, Stderr: os.Stderr,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Status != "success" {
		t.Fatalf("dind=%+v", res)
	}
	if _, err := os.Stat(filepath.Join(dir, ".glci", "logs", "dind.log")); err != nil {
		t.Fatal(err)
	}
}

func TestDockerFileVarsAndChildPipeline(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("docker not available")
	}
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
	if !dockerAvailable() {
		t.Skip("docker not available")
	}
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
		Stdout: os.Stdout, Stderr: os.Stderr,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 {
		t.Fatalf("res=%+v", res)
	}
	for _, r := range res {
		if r.Status != "success" {
			t.Fatalf("%s: %s", r.Name, r.Status)
		}
	}
}
