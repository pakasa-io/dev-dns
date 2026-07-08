package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Defaults used when scaffolding a new store (via `devdns init` or the global
// auto-init on first run). Port 1053 needs no privileges, so `devdns start`
// runs as your user; `devdns resolver install` then routes the zone to it. Use
// --port 53 for a whole-system resolver, which requires sudo to run.
const (
	defaultInitZone = "example.internal"
	defaultInitPort = 1053
)

// storeKind describes how the active store was chosen.
type storeKind int

const (
	kindProject  storeKind = iota // a project-local .devdns found by walking up
	kindGlobal                    // the ~/.devdns fallback
	kindExplicit                  // pointed at with --dir
)

func (k storeKind) String() string {
	switch k {
	case kindProject:
		return "project"
	case kindGlobal:
		return "global"
	case kindExplicit:
		return "explicit"
	default:
		return "unknown"
	}
}

// app holds the resolved paths for the active devdns store. Everything for a
// zone — records.yaml, the generated Corefile and zone files, and the CoreDNS
// pidfile/log — lives directly inside the store directory.
type app struct {
	dir         string    // the active store directory (~/.devdns, a project .devdns, or --dir)
	recordsPath string    // <dir>/records.yaml
	zonesDir    string    // <dir>/zones
	corefile    string    // <dir>/Corefile
	globalHome  string    // ~/.devdns: the shared CoreDNS binary and global store
	kind        storeKind // how dir was chosen
}

func newApp(dir, home string, kind storeKind) *app {
	return &app{
		dir:         dir,
		recordsPath: filepath.Join(dir, "records.yaml"),
		zonesDir:    filepath.Join(dir, "zones"),
		corefile:    filepath.Join(dir, "Corefile"),
		globalHome:  home,
		kind:        kind,
	}
}

// globalHome returns the global devdns directory: $DEVDNS_HOME if set, otherwise
// ~/.devdns. The result is absolute.
func globalHome() (string, error) {
	if h := strings.TrimSpace(os.Getenv("DEVDNS_HOME")); h != "" {
		return filepath.Abs(h)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory (set DEVDNS_HOME): %w", err)
	}
	return filepath.Join(home, ".devdns"), nil
}

// findProjectStore walks up from startDir looking for the nearest directory that
// contains a ".devdns" subdirectory, returning that ".devdns" path. This is how
// a project store takes precedence, git-style, from any subdirectory.
func findProjectStore(startDir string) (string, bool) {
	d := startDir
	for {
		cand := filepath.Join(d, ".devdns")
		if fi, err := os.Stat(cand); err == nil && fi.IsDir() {
			return cand, true
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", false
		}
		d = parent
	}
}

// resolve selects the active store. With --dir it uses that directory; otherwise
// it walks up from the current directory for a project .devdns and falls back to
// the global ~/.devdns.
func resolve(dirFlag string) (*app, error) {
	home, err := globalHome()
	if err != nil {
		return nil, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	return resolveFrom(dirFlag, cwd, home)
}

// resolveFrom is the pure core of resolve, with cwd and the global home passed in
// so it can be unit-tested without touching the process environment.
func resolveFrom(dirFlag, cwd, home string) (*app, error) {
	if strings.TrimSpace(dirFlag) != "" {
		abs, err := filepath.Abs(dirFlag)
		if err != nil {
			return nil, err
		}
		return newApp(abs, home, kindExplicit), nil
	}
	if store, ok := findProjectStore(cwd); ok && filepath.Clean(store) != filepath.Clean(home) {
		return newApp(store, home, kindProject), nil
	}
	return newApp(home, home, kindGlobal), nil
}

// defaultRecordsTemplate is the commented starter records.yaml written by `init`
// and the global auto-init. It takes the zone (%s) and listen port (%d).
const defaultRecordsTemplate = `# devdns records file -- the single source of truth for this dev zone.
#
# The zone is served authoritatively from the records below; every other query
# is forwarded to the upstream resolvers. Edit this file by hand, or with the
# devdns CLI (add / update / remove). Note: CLI edits rewrite this file and do
# not preserve comments.
zone: %s

# Default TTL (seconds) for records that do not set their own.
ttl: 60

# Listen port. 1053 needs no privileges (recommended): devdns runs as your user
# and "devdns resolver install" routes the zone to it. Use 53 for a whole-system
# resolver, which requires sudo to run. See the README.
port: %d

# Optional: bind to a single address (e.g. 127.0.0.1) to avoid exposing the
# resolver on your LAN. Omit to listen on all interfaces (required for Docker).
# address: 127.0.0.1

# Upstream resolvers for everything outside the local zone (Cloudflare + Google).
upstreams:
  - 1.1.1.1
  - 1.0.0.1
  - 8.8.8.8
  - 8.8.4.4

# Local records. name is relative to the zone; "@" is the zone apex.
records:
  - name: app
    type: A
    value: 127.0.0.1
`

// writeDefaultRecords writes a commented starter records.yaml at path, creating
// parent directories as needed.
func writeDefaultRecords(path, zone string, port int) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(fmt.Sprintf(defaultRecordsTemplate, zone, port)), 0o644)
}
