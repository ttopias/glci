package upgrade

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/ttopias/glci/internal/version"
)

const defaultRepo = "ttopias/glci"

// Options control glci upgrade.
type Options struct {
	Target  string // latest, v0.1.0, or empty (= latest)
	Dest    string // binary path; empty uses the running executable
	Check   bool
	Force   bool
	Current string
	Repo    string
	GOOS    string
	GOARCH  string
	Client  *http.Client
	API     string // GitHub API origin, default https://api.github.com
	Stdout  io.Writer
	Stderr  io.Writer
}

type release struct {
	TagName string  `json:"tag_name"`
	Assets  []asset `json:"assets"`
}

type asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

// Run upgrades the glci binary in place.
func Run(o Options) error {
	if o.Stdout == nil {
		o.Stdout = os.Stdout
	}
	if o.Stderr == nil {
		o.Stderr = os.Stderr
	}
	if o.Client == nil {
		o.Client = &http.Client{Timeout: 60 * time.Second}
	}
	if o.Repo == "" {
		o.Repo = defaultRepo
	}
	if o.API == "" {
		o.API = "https://api.github.com"
	}
	if o.GOOS == "" {
		o.GOOS = runtime.GOOS
	}
	if o.GOARCH == "" {
		o.GOARCH = runtime.GOARCH
	}
	if o.Current == "" {
		o.Current = version.String()
	}
	target := strings.TrimSpace(o.Target)
	if target == "" {
		target = "latest"
	}
	dest := o.Dest
	if dest == "" {
		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("locate glci binary: %w", err)
		}
		dest, err = filepath.EvalSymlinks(exe)
		if err != nil {
			dest = exe
		}
	}

	rel, err := fetchRelease(o, target)
	if err != nil {
		if o.Check {
			return fmt.Errorf("check latest: %w", err)
		}
		fmt.Fprintf(o.Stderr, "no GitHub release for %s; falling back to go install\n", target)
		return installWithGo(o, dest, target)
	}

	want := strings.TrimPrefix(rel.TagName, "v")
	have := strings.TrimPrefix(o.Current, "v")
	if o.Check {
		if !o.Force && have != "dev" && have == want {
			fmt.Fprintf(o.Stdout, "glci %s is already the latest\n", o.Current)
			return nil
		}
		fmt.Fprintf(o.Stdout, "glci %s → %s\n", o.Current, rel.TagName)
		return nil
	}
	if !o.Force && have != "dev" && have == want {
		fmt.Fprintf(o.Stdout, "already up to date (%s)\n", rel.TagName)
		return nil
	}

	url, name, err := assetURL(rel, o.GOOS, o.GOARCH)
	if err != nil {
		fmt.Fprintf(o.Stderr, "%v; falling back to go install\n", err)
		return installWithGo(o, dest, rel.TagName)
	}
	tmp, err := os.MkdirTemp("", "glci-upgrade-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	archive := filepath.Join(tmp, name)
	if err := download(o.Client, url, archive); err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	if sumURL := checksumsURL(rel); sumURL != "" {
		sums := filepath.Join(tmp, "checksums.txt")
		if err := download(o.Client, sumURL, sums); err != nil {
			return fmt.Errorf("download checksums: %w", err)
		}
		if err := verifyChecksum(archive, name, sums); err != nil {
			return err
		}
	} else {
		return fmt.Errorf("release %s has no checksums.txt", rel.TagName)
	}
	bin, err := extractBinary(archive, tmp)
	if err != nil {
		return err
	}
	if err := replaceBinary(bin, dest); err != nil {
		return err
	}
	fmt.Fprintf(o.Stdout, "upgraded %s to %s\n", dest, rel.TagName)
	return nil
}

func fetchRelease(o Options, target string) (*release, error) {
	u := o.API + "/repos/" + o.Repo + "/releases/latest"
	if target != "latest" {
		u = o.API + "/repos/" + o.Repo + "/releases/tags/" + target
	}
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "glci/"+o.Current)
	res, err := o.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github %s: %s", res.Status, strings.TrimSpace(string(body)))
	}
	var rel release
	if err := json.Unmarshal(body, &rel); err != nil {
		return nil, err
	}
	if rel.TagName == "" {
		return nil, fmt.Errorf("github release missing tag_name")
	}
	return &rel, nil
}

func assetURL(rel *release, goos, goarch string) (string, string, error) {
	ext := ".tar.gz"
	if goos == "windows" {
		ext = ".zip"
	}
	name := "glci_" + goos + "_" + goarch + ext
	for _, a := range rel.Assets {
		if a.Name == name {
			return a.URL, a.Name, nil
		}
	}
	return "", "", fmt.Errorf("no asset %s in %s", name, rel.TagName)
}

func checksumsURL(rel *release) string {
	for _, a := range rel.Assets {
		if a.Name == "checksums.txt" {
			return a.URL
		}
	}
	return ""
}

func verifyChecksum(archive, name, sumsPath string) error {
	b, err := os.ReadFile(sumsPath)
	if err != nil {
		return err
	}
	want := ""
	for _, line := range strings.Split(string(b), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[len(fields)-1] == name {
			want = fields[0]
			break
		}
	}
	if want == "" {
		return fmt.Errorf("no checksum for %s", name)
	}
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("checksum mismatch for %s", name)
	}
	return nil
}

func download(client *http.Client, url, dest string) error {
	res, err := client.Get(url)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%s", res.Status)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, res.Body)
	return err
}

func extractBinary(archive, dir string) (string, error) {
	if strings.HasSuffix(archive, ".zip") {
		return extractZip(archive, dir)
	}
	return extractTarGz(archive, dir)
}

func extractTarGz(archive, dir string) (string, error) {
	f, err := os.Open(archive)
	if err != nil {
		return "", err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		base := filepath.Base(hdr.Name)
		if strings.Contains(hdr.Name, "..") || hdr.Typeflag != tar.TypeReg || !isGlciName(base) {
			continue
		}
		out := filepath.Join(dir, "glci")
		w, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(w, tr); err != nil {
			w.Close()
			return "", err
		}
		w.Close()
		return out, nil
	}
	return "", fmt.Errorf("glci binary not found in archive")
}

func extractZip(archive, dir string) (string, error) {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return "", err
	}
	defer zr.Close()
	for _, f := range zr.File {
		base := filepath.Base(f.Name)
		if strings.Contains(f.Name, "..") || f.FileInfo().IsDir() || !isGlciName(base) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", err
		}
		out := filepath.Join(dir, "glci.exe")
		w, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			rc.Close()
			return "", err
		}
		_, copyErr := io.Copy(w, rc)
		w.Close()
		rc.Close()
		if copyErr != nil {
			return "", copyErr
		}
		return out, nil
	}
	return "", fmt.Errorf("glci binary not found in archive")
}

func isGlciName(base string) bool {
	return base == "glci" || base == "glci.exe"
}

func replaceBinary(src, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dest + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		os.Remove(tmp)
		return closeErr
	}
	if err := os.Rename(tmp, dest); err != nil {
		bak := dest + ".old"
		_ = os.Remove(bak)
		if err2 := os.Rename(dest, bak); err2 != nil {
			os.Remove(tmp)
			return err
		}
		if err := os.Rename(tmp, dest); err != nil {
			_ = os.Rename(bak, dest)
			return err
		}
		_ = os.Remove(bak)
	}
	return nil
}

func installWithGo(o Options, dest, target string) error {
	if _, err := exec.LookPath("go"); err != nil {
		return fmt.Errorf("no release asset and go is not installed; re-run install.sh")
	}
	mod := "github.com/" + o.Repo + "/cmd/glci"
	ver := "@latest"
	if target != "" && target != "latest" {
		ver = "@" + target
	}
	cmd := exec.Command("go", "install", mod+ver)
	cmd.Env = append(os.Environ(), "GOBIN="+filepath.Dir(dest))
	cmd.Stdout = o.Stdout
	cmd.Stderr = o.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go install: %w", err)
	}
	fmt.Fprintf(o.Stdout, "upgraded %s via go install%s\n", dest, ver)
	return nil
}
