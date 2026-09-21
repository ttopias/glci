package gitlabci

import (
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/ttopias/glci/internal/gitctx"
)

type RunContext struct {
	Git            gitctx.Info
	Source         string
	Extra          map[string]string
	Protected      bool
	OpenMRs        bool
	MRSource       string
	MRTarget       string
	PipelineID     string
	PipelineIID    string
	JobName        string
	JobStage       string
	JobID          string
	Matrix         map[string]string
	NodeIndex      int
	NodeTotal      int
	Dotenv         map[string]string
	Environment    string
	EnvironmentURL string
}

func Predefined(ctx RunContext) map[string]string {
	g := ctx.Git
	name := filepath.Base(g.Root)
	ns := "local"
	path := name
	if g.RemoteURL != "" {
		ns, path, name = parseRemote(g.RemoteURL)
	}
	if ctx.Source == "" {
		ctx.Source = "push"
		if ctx.MRSource != "" {
			ctx.Source = "merge_request_event"
		} else if g.Tag != "" {
			ctx.Source = "push"
		}
	}
	if ctx.PipelineID == "" {
		ctx.PipelineID = "1"
	}
	if ctx.PipelineIID == "" {
		ctx.PipelineIID = "1"
	}
	projectDir := "/builds/" + path
	if ctx.Git.Root != "" {
		// overwritten by executor to the real mount
	}
	vars := map[string]string{
		"CI":                              "true",
		"GITLAB_CI":                       "true",
		"CI_SERVER":                       "yes",
		"CI_SERVER_FQDN":                  "localhost",
		"CI_SERVER_HOST":                  "localhost",
		"CI_SERVER_NAME":                  "glci",
		"CI_SERVER_PORT":                  "80",
		"CI_SERVER_PROTOCOL":              "http",
		"CI_SERVER_SHELL_SSH_HOST":        "localhost",
		"CI_SERVER_SHELL_SSH_PORT":        "22",
		"CI_SERVER_REVISION":              "local",
		"CI_SERVER_URL":                   "http://localhost",
		"CI_SERVER_VERSION":               "18.0.0",
		"CI_SERVER_VERSION_MAJOR":         "18",
		"CI_SERVER_VERSION_MINOR":         "0",
		"CI_SERVER_VERSION_PATCH":         "0",
		"CI_API_V4_URL":                   "http://localhost/api/v4",
		"CI_API_GRAPHQL_URL":              "http://localhost/api/graphql",
		"CI_TEMPLATE_REGISTRY_HOST":       "registry.gitlab.com",
		"CI_PROJECT_DIR":                  projectDir,
		"CI_PROJECT_ID":                   "1",
		"CI_PROJECT_NAME":                 name,
		"CI_PROJECT_NAMESPACE":            ns,
		"CI_PROJECT_NAMESPACE_ID":         "1",
		"CI_PROJECT_PATH":                 path,
		"CI_PROJECT_PATH_SLUG":            slug(path),
		"CI_PROJECT_ROOT_NAMESPACE":       strings.Split(ns, "/")[0],
		"CI_PROJECT_TITLE":                name,
		"CI_PROJECT_URL":                  "http://localhost/" + path,
		"CI_PROJECT_VISIBILITY":           "private",
		"CI_PROJECT_REPOSITORY_LANGUAGES": "",
		"CI_DEFAULT_BRANCH":               g.DefaultBranch,
		"CI_COMMIT_SHA":                   g.SHA,
		"CI_COMMIT_SHORT_SHA":             g.ShortSHA,
		"CI_COMMIT_REF_NAME":              g.Ref,
		"CI_COMMIT_REF_SLUG":              slug(g.Ref),
		"CI_COMMIT_REF_PROTECTED":         boolCI(ctx.Protected),
		"CI_COMMIT_MESSAGE":               g.Message,
		"CI_COMMIT_TITLE":                 g.Title,
		"CI_COMMIT_DESCRIPTION":           strings.TrimPrefix(g.Message, g.Title),
		"CI_COMMIT_TIMESTAMP":             g.Timestamp,
		"CI_COMMIT_AUTHOR":                strings.TrimSpace(g.UserName + " <" + g.UserEmail + ">"),
		"CI_PIPELINE_ID":                  ctx.PipelineID,
		"CI_PIPELINE_IID":                 ctx.PipelineIID,
		"CI_PIPELINE_SOURCE":              ctx.Source,
		"CI_PIPELINE_URL":                 "http://localhost/" + path + "/-/pipelines/" + ctx.PipelineID,
		"CI_PIPELINE_CREATED_AT":          g.Timestamp,
		"CI_JOB_ID":                       ctx.JobID,
		"CI_JOB_NAME":                     ctx.JobName,
		"CI_JOB_NAME_SLUG":                slug(ctx.JobName),
		"CI_JOB_STAGE":                    ctx.JobStage,
		"CI_JOB_URL":                      "http://localhost/" + path + "/-/jobs/" + ctx.JobID,
		"CI_JOB_TIMEOUT":                  "3600",
		"CI_JOB_STARTED_AT":               g.Timestamp,
		"CI_REGISTRY":                     "localhost:5000",
		"CI_REGISTRY_IMAGE":               "localhost:5000/" + path,
		"CI_PAGES_DOMAIN":                 "localhost",
		"CI_PAGES_URL":                    "http://" + name + ".localhost",
		"GITLAB_USER_LOGIN":               g.UserName,
		"GITLAB_USER_NAME":                g.UserName,
		"GITLAB_USER_EMAIL":               g.UserEmail,
		"GITLAB_USER_ID":                  "1",
		"CI_CONFIG_PATH":                  ".gitlab-ci.yml",
		"CI_BUILDS_DIR":                   "/builds",
	}
	if g.Branch != "" {
		vars["CI_COMMIT_BRANCH"] = g.Branch
	}
	if g.Tag != "" {
		vars["CI_COMMIT_TAG"] = g.Tag
		vars["CI_COMMIT_TAG_MESSAGE"] = g.Message
	}
	if ctx.Source == "merge_request_event" || ctx.MRSource != "" {
		src := ctx.MRSource
		if src == "" {
			src = g.Branch
		}
		tgt := ctx.MRTarget
		if tgt == "" {
			tgt = g.DefaultBranch
		}
		vars["CI_PIPELINE_SOURCE"] = "merge_request_event"
		vars["CI_MERGE_REQUEST_ID"] = "1"
		vars["CI_MERGE_REQUEST_IID"] = "1"
		vars["CI_MERGE_REQUEST_REF_PATH"] = "refs/merge-requests/1/head"
		vars["CI_MERGE_REQUEST_PROJECT_ID"] = "1"
		vars["CI_MERGE_REQUEST_PROJECT_PATH"] = path
		vars["CI_MERGE_REQUEST_PROJECT_URL"] = vars["CI_PROJECT_URL"]
		vars["CI_MERGE_REQUEST_SOURCE_BRANCH_NAME"] = src
		vars["CI_MERGE_REQUEST_SOURCE_BRANCH_SHA"] = g.SHA
		vars["CI_MERGE_REQUEST_SOURCE_PROJECT_ID"] = "1"
		vars["CI_MERGE_REQUEST_SOURCE_PROJECT_PATH"] = path
		vars["CI_MERGE_REQUEST_SOURCE_PROJECT_URL"] = vars["CI_PROJECT_URL"]
		vars["CI_MERGE_REQUEST_TARGET_BRANCH_NAME"] = tgt
		vars["CI_MERGE_REQUEST_TARGET_BRANCH_SHA"] = g.SHA
		vars["CI_MERGE_REQUEST_TITLE"] = g.Title
		vars["CI_MERGE_REQUEST_EVENT_TYPE"] = "detached"
		vars["CI_OPEN_MERGE_REQUESTS"] = path + "!1"
	}
	if ctx.OpenMRs {
		vars["CI_OPEN_MERGE_REQUESTS"] = path + "!1"
	}
	if ctx.Protected {
		vars["CI_COMMIT_REF_PROTECTED"] = "true"
	}
	if ctx.NodeTotal > 0 {
		vars["CI_NODE_INDEX"] = strconv.Itoa(ctx.NodeIndex)
		vars["CI_NODE_TOTAL"] = strconv.Itoa(ctx.NodeTotal)
	}
	if ctx.Environment != "" {
		vars["CI_ENVIRONMENT_NAME"] = ctx.Environment
		vars["CI_ENVIRONMENT_SLUG"] = slug(ctx.Environment)
		vars["CI_ENVIRONMENT_URL"] = ctx.EnvironmentURL
	}
	for k, v := range ctx.Matrix {
		vars[k] = v
	}
	for k, v := range ctx.Dotenv {
		vars[k] = v
	}
	for k, v := range ctx.Extra {
		vars[k] = v
	}
	for _, e := range os.Environ() {
		k, v, ok := strings.Cut(e, "=")
		if !ok {
			continue
		}
		if strings.HasPrefix(k, "GLCI_") {
			vars[strings.TrimPrefix(k, "GLCI_")] = v
		}
	}
	return vars
}

func parseRemote(raw string) (ns, path, name string) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimSuffix(raw, ".git")
	if strings.HasPrefix(raw, "git@") {
		_, rest, _ := strings.Cut(raw, ":")
		path = rest
	} else if u, err := url.Parse(raw); err == nil && u.Path != "" {
		path = strings.TrimPrefix(u.Path, "/")
	} else {
		path = raw
	}
	path = strings.Trim(path, "/")
	name = filepath.Base(path)
	ns = strings.TrimSuffix(path, "/"+name)
	if ns == "" {
		ns = "local"
	}
	return ns, path, name
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	s = strings.ToLower(s)
	s = slugRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 63 {
		s = s[:63]
	}
	return s
}

func boolCI(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func Expand(s string, vars map[string]string) string {
	prev := ""
	for i := 0; i < 10 && s != prev; i++ {
		prev = s
		s = expandOnce(s, vars)
	}
	return s
}

var (
	reBrace = regexp.MustCompile(`\$\{([A-Za-z0-9_]+)\}`)
	rePlain = regexp.MustCompile(`\$([A-Za-z0-9_]+)`)
)

func expandOnce(s string, vars map[string]string) string {
	s = reBrace.ReplaceAllStringFunc(s, func(m string) string {
		name := reBrace.FindStringSubmatch(m)[1]
		if v, ok := vars[name]; ok {
			return v
		}
		return m
	})
	s = rePlain.ReplaceAllStringFunc(s, func(m string) string {
		name := m[1:]
		if v, ok := vars[name]; ok {
			return v
		}
		return m
	})
	return s
}

func ExpandMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	for i := 0; i < 10; i++ {
		changed := false
		for k, v := range out {
			n := Expand(v, out)
			if n != v {
				out[k] = n
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	return out
}

func ExpandAny(v any, vars map[string]string) any {
	switch n := v.(type) {
	case string:
		return Expand(n, vars)
	case map[string]any:
		out := make(map[string]any, len(n))
		for k, val := range n {
			out[Expand(k, vars)] = ExpandAny(val, vars)
		}
		return out
	case []any:
		out := make([]any, len(n))
		for i, val := range n {
			out[i] = ExpandAny(val, vars)
		}
		return out
	default:
		return n
	}
}

func NewID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func variablesFrom(v any) map[string]string {
	m, ok := asMap(v)
	if !ok {
		return map[string]string{}
	}
	out := map[string]string{}
	for k, val := range m {
		if nested, ok := asMap(val); ok {
			if asBool(nested["file"], false) {
				continue
			}
			out[k] = asString(nested["value"])
			continue
		}
		out[k] = asString(val)
	}
	return out
}
