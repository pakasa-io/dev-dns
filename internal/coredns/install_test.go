package coredns

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDownloadURL(t *testing.T) {
	got := downloadURL("1.11.3", "darwin", "arm64")
	want := "https://github.com/coredns/coredns/releases/download/v1.11.3/coredns_1.11.3_darwin_arm64.tgz"
	if got != want {
		t.Errorf("downloadURL = %q; want %q", got, want)
	}
	if u := downloadURL("1.11.3", "windows", "amd64"); !strings.HasSuffix(u, "coredns_1.11.3_windows_amd64.tgz") {
		t.Errorf("windows URL = %q", u)
	}
}

func TestDefaultDest(t *testing.T) {
	got := DefaultDest("/proj")
	wantBase := "coredns"
	if runtime.GOOS == "windows" {
		wantBase = "coredns.exe"
	}
	if filepath.Base(got) != wantBase || filepath.Base(filepath.Dir(got)) != "bin" {
		t.Errorf("DefaultDest = %q; want <workdir>/bin/%s", got, wantBase)
	}
}

// tarGz builds a gzipped tar archive from name->content entries.
func tarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtractCoreDNS(t *testing.T) {
	// A decoy file plus the binary under a nested path, as some archives nest.
	archive := tarGz(t, map[string]string{
		"README.md":                    "docs",
		"coredns_1.11.3_linux/coredns": "BINARY-CONTENT",
	})
	dir := t.TempDir()
	dest := filepath.Join(dir, "coredns")
	if err := extractCoreDNS(bytes.NewReader(archive), dest); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "BINARY-CONTENT" {
		t.Errorf("extracted content = %q; want BINARY-CONTENT", got)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(dest)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm()&0o100 == 0 {
			t.Errorf("extracted binary is not executable: mode %v", fi.Mode())
		}
	}
}

func TestExtractCoreDNSMissing(t *testing.T) {
	archive := tarGz(t, map[string]string{"notes.txt": "nothing here"})
	dir := t.TempDir()
	if err := extractCoreDNS(bytes.NewReader(archive), filepath.Join(dir, "coredns")); err == nil {
		t.Error("expected an error when the archive has no coredns binary")
	}
}
