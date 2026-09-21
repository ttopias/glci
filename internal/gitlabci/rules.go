package gitlabci

import (
	"path"
	"path/filepath"
	"strings"

	"github.com/ttopias/glci/internal/expr"
	"github.com/ttopias/glci/internal/gitctx"
)

type ruleResult struct {
	Match         bool
	When          string
	AllowFailure  *bool
	Variables     map[string]string
	Needs         any
	Interruptible *bool
	StartIn       string
}

func evalRules(rules []any, vars map[string]string, git gitctx.Info, root string) ruleResult {
	if len(rules) == 0 {
		return ruleResult{Match: true, When: "on_success"}
	}
	for _, raw := range rules {
		m, ok := asMap(raw)
		if !ok {
			continue
		}
		ifExpr := asString(m["if"])
		hasIf := m["if"] != nil
		changes := asStringSlice(m["changes"])
		if cm, ok := asMap(m["changes"]); ok {
			changes = asStringSlice(cm["paths"])
		}
		exists := asStringSlice(m["exists"])
		if em, ok := asMap(m["exists"]); ok {
			exists = asStringSlice(em["paths"])
		}

		okIf := true
		if hasIf {
			okIf, _ = expr.Eval(ifExpr, vars)
		}
		okChanges := true
		if len(changes) > 0 {
			okChanges = anyChanged(changes, git.ChangedFiles)
		}
		okExists := true
		if len(exists) > 0 {
			okExists = anyExists(exists, root)
		}
		if okIf && okChanges && okExists {
			when := asString(m["when"])
			if when == "" {
				when = "on_success"
			}
			res := ruleResult{
				Match:     true,
				When:      when,
				Variables: variablesFrom(m["variables"]),
				Needs:     m["needs"],
				StartIn:   asString(m["start_in"]),
			}
			if v, ok := m["allow_failure"]; ok {
				b := asBool(v, false)
				res.AllowFailure = &b
			}
			if v, ok := m["interruptible"]; ok {
				b := asBool(v, false)
				res.Interruptible = &b
			}
			return res
		}
	}
	return ruleResult{Match: false, When: "never"}
}

func anyChanged(patterns, files []string) bool {
	if len(files) == 0 {
		// GitLab treats no-diff (empty repo / first commit) as changed.
		return true
	}
	for _, f := range files {
		for _, p := range patterns {
			if matchGlob(p, f) {
				return true
			}
		}
	}
	return false
}

func anyExists(patterns []string, root string) bool {
	for _, p := range patterns {
		p = strings.TrimPrefix(p, "/")
		matches, _ := filepath.Glob(filepath.Join(root, filepath.FromSlash(p)))
		if len(matches) > 0 {
			return true
		}
	}
	return false
}

func matchGlob(pattern, name string) bool {
	pattern = strings.TrimPrefix(pattern, "/")
	name = strings.TrimPrefix(name, "/")
	ok, err := path.Match(pattern, name)
	if err == nil && ok {
		return true
	}
	// recursive ** support (simple)
	if strings.Contains(pattern, "**") {
		parts := strings.SplitN(pattern, "**", 2)
		prefix := strings.TrimSuffix(parts[0], "/")
		suffix := strings.TrimPrefix(parts[1], "/")
		if prefix != "" && !strings.HasPrefix(name, prefix) {
			return false
		}
		if suffix == "" {
			return true
		}
		ok, _ = path.Match(suffix, path.Base(name))
		if ok {
			return true
		}
		ok, _ = path.Match("*"+suffix, name)
		return ok
	}
	ok, _ = path.Match(pattern, path.Base(name))
	return ok
}

func evalOnlyExcept(job map[string]any, vars map[string]string, git gitctx.Info) bool {
	if job["rules"] != nil {
		return true
	}
	if only := job["only"]; only != nil {
		if !matchRefs(only, vars, git, true) {
			return false
		}
	}
	if except := job["except"]; except != nil {
		if matchRefs(except, vars, git, false) {
			return false
		}
	}
	return true
}

func matchRefs(spec any, vars map[string]string, git gitctx.Info, only bool) bool {
	switch s := spec.(type) {
	case string:
		return refMatches(s, vars, git)
	case []any:
		for _, item := range s {
			if refMatches(asString(item), vars, git) {
				return true
			}
		}
		return false
	default:
		m, ok := asMap(spec)
		if !ok {
			return only
		}
		if refs := asStringSlice(m["refs"]); len(refs) > 0 {
			hit := false
			for _, r := range refs {
				if refMatches(r, vars, git) {
					hit = true
					break
				}
			}
			if !hit {
				return false
			}
		}
		if vs := asStringSlice(m["variables"]); len(vs) > 0 {
			hit := false
			for _, exprSrc := range vs {
				ok, _ := expr.Eval(exprSrc, vars)
				if ok {
					hit = true
					break
				}
			}
			if !hit {
				return false
			}
		}
		if ch := asStringSlice(m["changes"]); len(ch) > 0 {
			if !anyChanged(ch, git.ChangedFiles) {
				return false
			}
		}
		return true
	}
}

func refMatches(ref string, vars map[string]string, git gitctx.Info) bool {
	switch ref {
	case "branches":
		return git.Branch != ""
	case "tags":
		return git.Tag != ""
	case "merge_requests":
		return vars["CI_PIPELINE_SOURCE"] == "merge_request_event"
	case "api", "web", "schedules", "pipelines", "triggers", "external", "chats", "external_pull_requests", "pushes":
		src := vars["CI_PIPELINE_SOURCE"]
		mapSrc := map[string]string{
			"api": "api", "web": "web", "schedules": "schedule", "pipelines": "pipeline",
			"triggers": "trigger", "pushes": "push", "external": "external",
			"chats": "chat", "external_pull_requests": "external_pull_request_event",
		}
		return src == mapSrc[ref]
	default:
		if git.Ref == ref || git.Branch == ref || git.Tag == ref {
			return true
		}
		ok, _ := path.Match(ref, git.Ref)
		return ok
	}
}
