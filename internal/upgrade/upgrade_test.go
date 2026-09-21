package upgrade

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpgradeFromRelease(t *testing.T) {
	archive := makeTarGz(t, "glci", []byte("new-glci-binary"))
	srv := newReleaseServer(t, "/repos/ttopias/glci/releases/latest", "v0.2.0", "glci_linux_amd64.tar.gz", archive)
	dest := filepath.Join(t.TempDir(), "glci")
	if err := os.WriteFile(dest, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := Run(Options{
		Current: "0.1.0",
		Dest:    dest,
		GOOS:    "linux",
		GOARCH:  "amd64",
		Client:  srv.Client(),
		API:     srv.URL,
		Stdout:  &out,
		Stderr:  io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new-glci-binary" {
		t.Fatalf("dest=%q", got)
	}
	if !strings.Contains(out.String(), "v0.2.0") {
		t.Fatalf("stdout=%s", out.String())
	}
}

func TestUpgradeAlreadyLatest(t *testing.T) {
	srv := newReleaseServer(t, "/repos/ttopias/glci/releases/latest", "v0.2.0", "glci_linux_amd64.tar.gz", nil)
	dest := filepath.Join(t.TempDir(), "glci")
	if err := os.WriteFile(dest, []byte("same"), 0o755); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := Run(Options{
		Current: "0.2.0",
		Dest:    dest,
		Client:  srv.Client(),
		API:     srv.URL,
		Stdout:  &out,
		Stderr:  io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dest)
	if string(got) != "same" {
		t.Fatalf("rewrote binary: %q", got)
	}
	if !strings.Contains(out.String(), "already up to date") {
		t.Fatalf("stdout=%s", out.String())
	}
}

func TestUpgradeCheck(t *testing.T) {
	srv := newReleaseServer(t, "/repos/ttopias/glci/releases/latest", "v1.0.0", "glci_linux_amd64.tar.gz", nil)
	var out bytes.Buffer
	err := Run(Options{
		Check:   true,
		Current: "0.1.0",
		Dest:    filepath.Join(t.TempDir(), "glci"),
		Client:  srv.Client(),
		API:     srv.URL,
		Stdout:  &out,
		Stderr:  io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "0.1.0 → v1.0.0") {
		t.Fatalf("stdout=%s", out.String())
	}
}

func TestUpgradePinnedTag(t *testing.T) {
	archive := makeTarGz(t, "./glci", []byte("pinned"))
	srv := newReleaseServer(t, "/repos/ttopias/glci/releases/tags/v0.1.0", "v0.1.0", "glci_darwin_arm64.tar.gz", archive)
	dest := filepath.Join(t.TempDir(), "bin", "glci")
	var out bytes.Buffer
	err := Run(Options{
		Target:  "v0.1.0",
		Current: "dev",
		Dest:    dest,
		GOOS:    "darwin",
		GOARCH:  "arm64",
		Client:  srv.Client(),
		API:     srv.URL,
		Stdout:  &out,
		Stderr:  io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "pinned" {
		t.Fatalf("dest=%q", got)
	}
}

func newReleaseServer(t *testing.T, apiPath, tag, assetName string, archive []byte) *httptest.Server {
	t.Helper()
	sum := sha256.Sum256(archive)
	sumsBody := fmt.Sprintf("%x  %s\n", sum, assetName)
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc(apiPath, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{
			"tag_name": "`+tag+`",
			"assets": [
				{"name": "`+assetName+`", "browser_download_url": "`+srv.URL+`/dl"},
				{"name": "checksums.txt", "browser_download_url": "`+srv.URL+`/sums"}
			]
		}`)
	})
	mux.HandleFunc("/dl", func(w http.ResponseWriter, r *http.Request) {
		if archive == nil {
			http.NotFound(w, r)
			return
		}
		w.Write(archive)
	})
	mux.HandleFunc("/sums", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, sumsBody)
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestUpgradeZipWindowsAsset(t *testing.T) {
	archive := makeZip(t, "glci.exe", []byte("win-bin"))
	srv := newReleaseServer(t, "/repos/ttopias/glci/releases/latest", "v3.0.0", "glci_windows_amd64.zip", archive)
	dest := filepath.Join(t.TempDir(), "glci.exe")
	err := Run(Options{
		Current: "dev",
		Dest:    dest,
		GOOS:    "windows",
		GOARCH:  "amd64",
		Client:  srv.Client(),
		API:     srv.URL,
		Stdout:  io.Discard,
		Stderr:  io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "win-bin" {
		t.Fatalf("dest=%q", got)
	}
}

func TestUpgradeMissingReleaseFallsBackMessage(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/ttopias/glci/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "missing", http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	var stderr bytes.Buffer
	err := Run(Options{
		Check:   true,
		Current: "dev",
		Dest:    filepath.Join(t.TempDir(), "glci"),
		Client:  srv.Client(),
		API:     srv.URL,
		Stdout:  io.Discard,
		Stderr:  &stderr,
	})
	if err == nil {
		t.Fatal("expected check error when no release")
	}
}

func TestUpgradeChecksumMismatch(t *testing.T) {
	archive := makeTarGz(t, "glci", []byte("bin"))
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/repos/ttopias/glci/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{
			"tag_name": "v1.0.0",
			"assets": [
				{"name": "glci_linux_amd64.tar.gz", "browser_download_url": "`+srv.URL+`/dl"},
				{"name": "checksums.txt", "browser_download_url": "`+srv.URL+`/sums"}
			]
		}`)
	})
	mux.HandleFunc("/dl", func(w http.ResponseWriter, r *http.Request) { w.Write(archive) })
	mux.HandleFunc("/sums", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "0000000000000000000000000000000000000000000000000000000000000000  glci_linux_amd64.tar.gz\n")
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	err := Run(Options{
		Current: "dev",
		Dest:    filepath.Join(t.TempDir(), "glci"),
		GOOS:    "linux",
		GOARCH:  "amd64",
		Client:  srv.Client(),
		API:     srv.URL,
		Stdout:  io.Discard,
		Stderr:  io.Discard,
	})
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("err=%v", err)
	}
}

func makeZip(t *testing.T, name string, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func makeTarGz(t *testing.T, name string, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0755, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
