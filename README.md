# glci

Run GitLab CI/CD pipelines on your machine, in Docker. No GitLab server.

`glci` compiles `.gitlab-ci.yml` the way GitLab does — includes, extends, `!reference`, rules, matrix, needs, artifacts, services, docker-in-docker — then runs created jobs in local containers.

Default `glci` (or `glci run`) runs every created job except `when: manual`. Each run refreshes `.glci/` (logs, artifacts, report). Job workspace copies and other temp files are removed when the run finishes. Pass `--manual` to include those jobs, or `--job NAME` to run a specific manual job.

## Install / upgrade

Same pattern as [golangci-lint](https://golangci-lint.run/welcome/install/):

```bash
# install.sh (GitHub release, or go install if none exists yet)
curl -sSfL https://raw.githubusercontent.com/ttopias/glci/main/install.sh | sh -s -- -b $(go env GOPATH)/bin

# pin a version
curl -sSfL https://raw.githubusercontent.com/ttopias/glci/main/install.sh | sh -s -- -b $(go env GOPATH)/bin v0.1.0
```

```bash
# Go 1.27+
go install github.com/ttopias/glci/cmd/glci@latest
```

```bash
# from a clone
git clone https://github.com/ttopias/glci
cd glci
make install
```

Needs [Go 1.27+](https://go.dev/dl/) (this repo pins `toolchain go1.27.1`) and [Docker](https://docs.docker.com/get-docker/). After that, upgrade in place:

```bash
glci upgrade              # latest GitHub release (or go install @latest)
glci upgrade v0.1.0       # pin a tag
glci upgrade --check      # print current vs latest, do not install
glci version
```

## Usage

```bash
glci                      # created jobs (skip when:manual)
glci run                  # same
glci list                 # jobs that would run
glci compile              # expanded pipeline YAML
glci upgrade              # replace this binary with the latest release
glci run --manual         # also run when:manual jobs
glci run --job unit       # one job plus its needs
glci run --mr             # merge-request pipeline
glci run --var FOO=bar
```

After a run:

| Path | Contents |
| --- | --- |
| `.glci/logs/<job>.log` | Full job log |
| `.glci/artifacts/<job>/` | Job artifacts (saved even when the job failed) |
| `.glci/report.json` | Status, log path, and artifact path per job |
| `.glci/cache/` | Job caches (kept across runs) |
| `.glci/pages/` | Pages output when a pages job ran |

`.glci/tmp/` and `.glci/builds/` are used while jobs run and are deleted afterward.

## Docker CLI (no pipeline extras)

Like a typical remote GitLab docker runner, glci binds the **host Docker socket** into every job and points the Docker CLI at it. You do **not** need a `docker:*-dind` service or `DOCKER_HOST` / `DOCKER_TLS_CERTDIR` in `.gitlab-ci.yml`.

```yaml
image: my.registry/ci-with-docker:latest   # or docker:29, etc.
script:
  - docker build -t app .
  - docker run --rm app
```

Copied GitLab templates that still list `services: docker:*-dind` or `DOCKER_HOST=tcp://docker:2375` still use the host socket; nested DinD is not started.

## Tests

```bash
go test ./...
```

Compiler tests cover includes (local / project / template / component), `spec:inputs`, anchors, `!reference`, `default`, `extends`, `inherit`, rules, `only`/`except`, workflow, needs, matrix, artifacts/dotenv, cache, retry, timeout, environments, coverage, pages, child pipelines, file variables, secrets/`id_tokens`.

Docker e2e tests (skipped only if `docker info` fails locally) cover custom images, services, artifacts, coverage, and Docker CLI via the host socket. On GitHub Actions a missing daemon fails the job instead of skipping.

## Local config (`.glci.yml`)

GitLab `include:project`, `include:template`, and `include:component` normally talk to GitLab. Map them to directories:

```yaml
variables:
  REGISTRY_PASSWORD: "set me"

projects:
  mygroup/ci-templates: ../ci-templates

components:
  gitlab.com/my-org/secret-detection: ./vendor/secret-detection

templates_dir: ./vendor/gitlab-templates
executor: docker
manual: skip           # default; run = --manual
protected_branches: [main]
```

`include:remote` is off unless you pass `--allow-remote` or set `allow_remote: true`. Prefer vendoring those files.

File-type variables, secrets, and `id_tokens` are filled from `GLCI_SECRET_<NAME>` / dummy JWTs. There is no Vault/GCP/Azure network call.

## Pipeline features

**Global:** `default`, `include` (local / project / template / component / remote), `stages`, `variables` (including `file: true`), `workflow`, `spec:inputs`.

**Jobs:** `script`, `run`, `before_script`, `after_script`, `hooks:pre_get_sources_script`, `image` (name, entrypoint, pull_policy, docker.user/platform/privileged), `services` (alias, command, entrypoint, variables), `stage`, `needs`, `dependencies`, `rules`, `only`/`except`, `when`, `allow_failure`, `retry`, `timeout`, `start_in`, `parallel` / `parallel:matrix`, `artifacts` (paths, exclude, untracked, reports:dotenv), `cache` (key, key:files, fallback_keys, policy), `extends`, `inherit`, `environment`, `coverage`, `tags` (ignored for scheduling), `resource_group`, `interruptible`, `pages` / `publish`, `release`, `trigger` / child pipelines (`include` + `strategy:depend`), `secrets`, `id_tokens`.

**YAML:** anchors, merge keys, `!reference`, nested includes, `include:rules`, `$[[ inputs.x ]]`.

## GitLab-server-only bits

These need a GitLab instance. glci approximates or skips them:

| Feature | Local behavior |
| --- | --- |
| Runner tags / shared runners | Every job can run |
| Merge request widgets | `--mr` sets MR predefined variables |
| Protected branches | `.glci.yml` `protected_branches` |
| Cross-project `needs:project` | Map the project in `.glci.yml` |
| Real OIDC / Vault secrets | Env vars / dummy JWT |
| Kubernetes executor | Docker instead |
| GitLab Pages hosting | Files copied to `.glci/pages` |
