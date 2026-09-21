package gitctx

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Info is git metadata used to populate predefined CI variables.
type Info struct {
	Root          string
	SHA           string
	ShortSHA      string
	Ref           string
	Branch        string
	Tag           string
	Message       string
	Title         string
	Timestamp     string
	DefaultBranch string
	RemoteURL     string
	UserName      string
	UserEmail     string
	ChangedFiles  []string
}

func Detect(dir string) (Info, error) {
	root, err := git(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		abs, _ := filepath.Abs(dir)
		return Info{Root: abs, DefaultBranch: "main"}, nil
	}
	info := Info{Root: root}
	info.SHA, _ = git(root, "rev-parse", "HEAD")
	if len(info.SHA) >= 8 {
		info.ShortSHA = info.SHA[:8]
	}
	info.Ref, _ = git(root, "symbolic-ref", "--short", "HEAD")
	if info.Ref == "" {
		info.Ref, _ = git(root, "rev-parse", "--abbrev-ref", "HEAD")
	}
	if info.Ref == "" || info.Ref == "HEAD" {
		info.Ref = detectDefaultBranch(root)
	}
	if info.Ref != "" && info.Ref != "HEAD" {
		info.Branch = info.Ref
	}
	if tag, err := git(root, "describe", "--tags", "--exact-match"); err == nil {
		info.Tag = tag
		info.Branch = ""
		info.Ref = tag
	}
	if info.SHA == "" {
		info.SHA = strings.Repeat("0", 40)
		info.ShortSHA = "00000000"
	}
	info.Message, _ = git(root, "log", "-1", "--pretty=%B")
	info.Message = strings.TrimSpace(info.Message)
	if i := strings.Index(info.Message, "\n"); i >= 0 {
		info.Title = strings.TrimSpace(info.Message[:i])
	} else {
		info.Title = info.Message
	}
	if ts, err := git(root, "log", "-1", "--pretty=%cI"); err == nil {
		info.Timestamp = ts
	} else {
		info.Timestamp = time.Now().Format(time.RFC3339)
	}
	info.DefaultBranch = detectDefaultBranch(root)
	info.RemoteURL, _ = git(root, "config", "--get", "remote.origin.url")
	info.UserName, _ = git(root, "config", "user.name")
	info.UserEmail, _ = git(root, "config", "user.email")
	info.ChangedFiles = changedFiles(root, info.DefaultBranch)
	return info, nil
}

func detectDefaultBranch(root string) string {
	if b, err := git(root, "symbolic-ref", "refs/remotes/origin/HEAD"); err == nil {
		return strings.TrimPrefix(b, "refs/remotes/origin/")
	}
	for _, name := range []string{"main", "master"} {
		if _, err := git(root, "rev-parse", "--verify", name); err == nil {
			return name
		}
	}
	if b, err := git(root, "rev-parse", "--abbrev-ref", "HEAD"); err == nil && b != "HEAD" {
		return b
	}
	return "main"
}

func changedFiles(root, defaultBranch string) []string {
	out, err := git(root, "diff", "--name-only", defaultBranch+"...HEAD")
	if err != nil {
		out, err = git(root, "diff", "--name-only", "HEAD~1")
	}
	if err != nil {
		out, _ = git(root, "status", "--porcelain", "-uall")
		var files []string
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			parts := strings.Fields(line)
			files = append(files, parts[len(parts)-1])
		}
		return files
	}
	var files []string
	for _, f := range strings.Split(out, "\n") {
		if f != "" {
			files = append(files, f)
		}
	}
	return files
}

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

func Exists(root, rel string) bool {
	_, err := os.Stat(filepath.Join(root, rel))
	return err == nil
}
