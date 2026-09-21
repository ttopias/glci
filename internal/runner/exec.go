package runner

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ttopias/glci/internal/gitlabci"
)

func runShell(opts Options, j gitlabci.Job, build, script string) (int, string, error) {
	shell := "bash"
	if _, err := exec.LookPath("bash"); err != nil {
		shell = "sh"
	}
	cmd := exec.Command(shell, script)
	cmd.Dir = build
	env := append([]string{}, os.Environ()...)
	env = append(env, envList(j.Variables)...)
	env = append(env, "CI_PROJECT_DIR="+build)
	cmd.Env = env
	var buf bytes.Buffer
	cmd.Stdout = io.MultiWriter(opts.Stdout, &buf)
	cmd.Stderr = io.MultiWriter(opts.Stderr, &buf)
	var err error
	if j.Timeout > 0 {
		_, err = runWithTimeout(cmd, j.Timeout)
	} else {
		err = cmd.Run()
	}
	return exitCode(err), buf.String(), err
}

func runDocker(opts Options, j gitlabci.Job, build, script string) (int, string, error) {
	if !dockerAvailable() {
		return 1, "", fmt.Errorf("docker is not running; start Docker Desktop / the daemon")
	}
	jobID := j.Variables["CI_JOB_ID"]
	if jobID == "" {
		jobID = gitlabci.NewID()
	}
	net := "glci-" + jobID
	if out, err := exec.Command("docker", "network", "create", net).CombinedOutput(); err != nil {
		if !strings.Contains(string(out), "already exists") {
			return 1, "", fmt.Errorf("docker network: %s", strings.TrimSpace(string(out)))
		}
	}
	defer func() { _ = exec.Command("docker", "network", "rm", net).Run() }()

	var svcNames []string
	defer func() {
		for i := len(svcNames) - 1; i >= 0; i-- {
			_ = exec.Command("docker", "rm", "-f", svcNames[i]).Run()
		}
	}()

	for i, svc := range j.Services {
		if isDindImage(svc.Name) {
			// Host daemon is the only Docker; nested docker:*-dind sidecars are unused.
			continue
		}
		id := fmt.Sprintf("glci-%s-svc-%d", jobID[:min(8, len(jobID))], i)
		alias := svc.Alias
		if alias == "" {
			alias = serviceAlias(svc.Name)
		}
		args := []string{"run", "-d", "--name", id, "--network", net, "--network-alias", alias}
		for _, a := range svc.AliasList {
			if a != "" && a != svc.Alias {
				args = append(args, "--network-alias", a)
			}
		}
		if opts.Privileged {
			args = append(args, "--privileged")
		}
		for k, v := range svc.Variables {
			args = append(args, "-e", k+"="+v)
		}
		if err := ensureImage(svc.Name, svc.PullPolicy); err != nil {
			return 1, "", fmt.Errorf("service image %s: %w", svc.Name, err)
		}
		if len(svc.Entrypoint) > 0 && svc.Entrypoint[0] != "" {
			args = append(args, "--entrypoint", svc.Entrypoint[0])
		}
		args = append(args, svc.Name)
		if len(svc.Entrypoint) > 1 {
			args = append(args, svc.Entrypoint[1:]...)
		}
		args = append(args, svc.Command...)
		cmd := exec.Command("docker", args...)
		cmd.Stdout = opts.Stdout
		cmd.Stderr = opts.Stderr
		if err := cmd.Run(); err != nil {
			return 1, "", fmt.Errorf("service %s: %w", svc.Name, err)
		}
		svcNames = append(svcNames, id)
		waitHealthy(id, 45*time.Second)
	}

	container := "glci-job-" + jobID
	if len(container) > 60 {
		container = container[:60]
	}
	defer func() { _ = exec.Command("docker", "rm", "-f", container).Run() }()

	projectDir := "/builds/" + j.Variables["CI_PROJECT_PATH"]
	if projectDir == "/builds/" || strings.HasSuffix(projectDir, "/builds/") {
		projectDir = "/builds/project"
	}
	j.Variables["CI_PROJECT_DIR"] = projectDir
	remapFileVariables(j, projectDir)
	dockerLogin(j)

	if err := ensureImage(j.Image.Name, j.Image.PullPolicy); err != nil {
		return 1, "", err
	}

	entrypoint := []string{"/bin/sh"}
	if j.Image != nil && len(j.Image.Entrypoint) > 0 && j.Image.Entrypoint[0] != "" {
		entrypoint = j.Image.Entrypoint
	}

	args := []string{"run", "--name", container, "--network", net, "-w", projectDir,
		"-v", abs(build) + ":" + projectDir,
		"-v", abs(script) + ":/glci/job.sh:ro",
		"--entrypoint", entrypoint[0],
	}
	args = bindHostDocker(args, j)
	if opts.Privileged {
		args = append(args, "--privileged")
	}
	if j.Image.Docker != nil {
		if p, ok := j.Image.Docker["privileged"].(bool); ok && p {
			args = append(args, "--privileged")
		}
		if u := asDockerString(j.Image.Docker["user"]); u != "" {
			args = append(args, "--user", u)
		}
		if p := asDockerString(j.Image.Docker["platform"]); p != "" {
			args = append(args, "--platform", p)
		}
	}
	for _, e := range envList(j.Variables) {
		args = append(args, "-e", e)
	}
	args = append(args, j.Image.Name)
	args = append(args, entrypoint[1:]...)
	args = append(args, "/glci/job.sh")

	cmd := exec.Command("docker", args...)
	var buf bytes.Buffer
	cmd.Stdout = io.MultiWriter(opts.Stdout, &buf)
	cmd.Stderr = io.MultiWriter(opts.Stderr, &buf)
	var err error
	if j.Timeout > 0 {
		_, err = runWithTimeout(cmd, j.Timeout)
	} else {
		err = cmd.Run()
	}
	return exitCode(err), buf.String(), err
}

func ensureImage(name, policy string) error {
	if name == "" {
		return fmt.Errorf("empty image name")
	}
	switch policy {
	case "never":
		return nil
	case "always":
		return dockerPull(name)
	default:
		if imageExists(name) {
			return nil
		}
		return dockerPull(name)
	}
}

func imageExists(name string) bool {
	err := exec.Command("docker", "image", "inspect", name).Run()
	return err == nil
}

func dockerPull(name string) error {
	cmd := exec.Command("docker", "pull", name)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func dockerLogin(j gitlabci.Job) {
	user := j.Variables["CI_REGISTRY_USER"]
	pass := j.Variables["CI_REGISTRY_PASSWORD"]
	registry := j.Variables["CI_REGISTRY"]
	if user == "" || pass == "" || registry == "" {
		return
	}
	if registry == "localhost:5000" || registry == "localhost" {
		return
	}
	cmd := exec.Command("docker", "login", "-u", user, "--password-stdin", registry)
	cmd.Stdin = strings.NewReader(pass)
	_ = cmd.Run()
}

func asDockerString(v any) string {
	s, _ := v.(string)
	return s
}

func waitHealthy(id string, d time.Duration) {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		out, err := exec.Command("docker", "inspect", "-f", "{{.State.Running}} {{.State.Status}}", id).Output()
		if err == nil && strings.Contains(string(out), "true") {
			time.Sleep(400 * time.Millisecond)
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func isDindImage(name string) bool {
	return strings.Contains(strings.ToLower(name), "dind")
}

// bindHostDocker mounts the host daemon, matching a typical GitLab docker runner.
// Official docker images default DOCKER_TLS_CERTDIR=/certs; always clear TLS for the socket.
func bindHostDocker(args []string, j gitlabci.Job) []string {
	applyHostDockerVars(j.Variables)
	sock := dockerSocketPath()
	if sock == "" {
		return args
	}
	return append(args, "-v", sock+":/var/run/docker.sock")
}

func applyHostDockerVars(m map[string]string) {
	if m == nil {
		return
	}
	m["DOCKER_HOST"] = "unix:///var/run/docker.sock"
	m["DOCKER_TLS_CERTDIR"] = ""
	delete(m, "DOCKER_CERT_PATH")
	delete(m, "DOCKER_TLS_VERIFY")
}

func existingFile(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

func unixSocketFile(host string) string {
	h := strings.TrimSpace(host)
	if !strings.HasPrefix(strings.ToLower(h), "unix://") {
		return ""
	}
	p := h[len("unix://"):]
	if p == "" || !filepath.IsAbs(p) {
		return ""
	}
	p = filepath.Clean(p)
	if existingFile(p) {
		return p
	}
	return ""
}

func dockerSocketPath() string {
	candidates := []string{"/var/run/docker.sock"}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates,
			filepath.Join(home, ".docker", "run", "docker.sock"),
			filepath.Join(home, ".colima", "default", "docker.sock"),
		)
	}
	ctx := ""
	if out, err := exec.Command("docker", "context", "inspect", "-f", "{{.Endpoints.docker.Host}}").Output(); err == nil {
		ctx = strings.TrimSpace(string(out))
	}
	return pickDockerSocket(os.Getenv("DOCKER_HOST"), ctx, candidates)
}

// pickDockerSocket chooses a host path that can be bind-mounted into job containers.
// /var/run/docker.sock is preferred when it exists: Docker Desktop on macOS exposes that
// path into the Linux VM, while unix://~/.docker/run/docker.sock is not mountable there.
func pickDockerSocket(envHost, contextHost string, candidates []string) string {
	if len(candidates) > 0 && existingFile(candidates[0]) {
		return candidates[0]
	}
	if p := unixSocketFile(envHost); p != "" {
		return p
	}
	if p := unixSocketFile(contextHost); p != "" {
		return p
	}
	for _, p := range candidates[1:] {
		if existingFile(p) {
			return p
		}
	}
	return ""
}

func serviceAlias(image string) string {
	s := image
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	if i := strings.Index(s, ":"); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return "docker"
	}
	return s
}

func runWithTimeout(cmd *exec.Cmd, d time.Duration) (int, error) {
	if err := cmd.Start(); err != nil {
		return 1, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return exitCode(err), err
	case <-time.After(d):
		_ = cmd.Process.Kill()
		return 1, fmt.Errorf("timeout after %s", d)
	}
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	return 1
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func abs(path string) string {
	a, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return a
}
