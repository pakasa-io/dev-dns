package coredns

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"time"
)

// DefaultVersion is the CoreDNS release downloaded when none is specified.
// Override per call, or with the DEVDNS_COREDNS_VERSION environment variable.
const DefaultVersion = "1.11.3"

// InstallOptions controls a CoreDNS download.
type InstallOptions struct {
	Version string       // CoreDNS version; defaults to DefaultVersion
	Dest    string       // target binary path (see DefaultDest)
	Log     func(string) // optional progress logger
}

// DefaultDest returns the conventional install path for the current platform:
// <workDir>/bin/coredns (coredns.exe on Windows). This matches FindBinary's
// search path, so an installed binary is found automatically afterwards.
func DefaultDest(workDir string) string {
	name := "coredns"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(workDir, "bin", name)
}

// EnsureBinary returns a usable coredns path. It first looks for an existing
// binary via FindBinary across searchDirs; if none is found and allowDownload is
// true, it downloads one into downloadDir/bin. A binary pinned explicitly (via
// the argument or $DEVDNS_COREDNS) is never overridden by a download.
func EnsureBinary(explicit string, searchDirs []string, downloadDir string, allowDownload bool, log func(string)) (string, error) {
	if p, err := FindBinary(explicit, searchDirs...); err == nil {
		return p, nil
	} else if !allowDownload || explicit != "" || os.Getenv("DEVDNS_COREDNS") != "" {
		return "", err
	}
	if log != nil {
		log("CoreDNS binary not found.")
	}
	return Install(InstallOptions{Dest: DefaultDest(downloadDir), Log: log})
}

// Install downloads the CoreDNS release for the current OS/architecture and
// writes the binary to opts.Dest, returning its path. It is self-contained
// (net/http + archive/tar + compress/gzip), so it needs no external curl or tar
// and works on any platform CoreDNS publishes a release for.
func Install(opts InstallOptions) (string, error) {
	if opts.Dest == "" {
		return "", fmt.Errorf("install: destination path is required")
	}
	version := opts.Version
	if version == "" {
		version = DefaultVersion
		if v := os.Getenv("DEVDNS_COREDNS_VERSION"); v != "" {
			version = v
		}
	}
	logf := opts.Log
	if logf == nil {
		logf = func(string) {}
	}

	url := downloadURL(version, runtime.GOOS, runtime.GOARCH)
	logf(fmt.Sprintf("Downloading CoreDNS %s (%s/%s)...", version, runtime.GOOS, runtime.GOARCH))

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "devdns-installer")

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("downloading coredns: %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return "", fmt.Errorf("no CoreDNS %s release for %s/%s; install it manually or set DEVDNS_COREDNS (%s)",
			version, runtime.GOOS, runtime.GOARCH, url)
	default:
		return "", fmt.Errorf("downloading coredns: unexpected status %s from %s", resp.Status, url)
	}

	if err := os.MkdirAll(filepath.Dir(opts.Dest), 0o755); err != nil {
		return "", err
	}
	if err := extractCoreDNS(resp.Body, opts.Dest); err != nil {
		return "", err
	}
	logf(fmt.Sprintf("Installed CoreDNS to %s", opts.Dest))
	return opts.Dest, nil
}

func downloadURL(version, goos, goarch string) string {
	asset := fmt.Sprintf("coredns_%s_%s_%s.tgz", version, goos, goarch)
	return fmt.Sprintf("https://github.com/coredns/coredns/releases/download/v%s/%s", version, asset)
}

// extractCoreDNS reads a gzipped tar stream and writes the coredns executable
// it contains to dest.
func extractCoreDNS(r io.Reader, dest string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("gunzip coredns archive: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("reading coredns archive: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if base := path.Base(hdr.Name); base == "coredns" || base == "coredns.exe" {
			return writeExecutable(tr, dest)
		}
	}
	return fmt.Errorf("coredns binary not found in archive")
}

// writeExecutable copies r to dest atomically (temp file + rename) with the
// executable bit set.
func writeExecutable(r io.Reader, dest string) error {
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".coredns-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	_, copyErr := io.Copy(tmp, r)
	closeErr := tmp.Close()
	if copyErr != nil || closeErr != nil {
		os.Remove(tmpName)
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, dest); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}
