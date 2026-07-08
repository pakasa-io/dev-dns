package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGlobalHome(t *testing.T) {
	custom := t.TempDir()
	t.Setenv("DEVDNS_HOME", custom)
	got, err := globalHome()
	if err != nil {
		t.Fatal(err)
	}
	if got != custom {
		t.Errorf("globalHome() with DEVDNS_HOME = %q; want %q", got, custom)
	}

	t.Setenv("DEVDNS_HOME", "")
	got, err = globalHome()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got) != ".devdns" {
		t.Errorf("globalHome() default = %q; want a path ending in .devdns", got)
	}
}

func TestFindProjectStore(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "a")
	store := filepath.Join(a, ".devdns")
	deep := filepath.Join(a, "b", "c")
	mustMkdirAll(t, store)
	mustMkdirAll(t, deep)

	// Found by walking up from a nested subdirectory.
	got, ok := findProjectStore(deep)
	if !ok || got != store {
		t.Fatalf("findProjectStore(%q) = %q, %v; want %q, true", deep, got, ok, store)
	}

	// The nearest .devdns wins over an ancestor's.
	mustMkdirAll(t, filepath.Join(root, ".devdns"))
	if got, ok := findProjectStore(deep); !ok || got != store {
		t.Errorf("nearest .devdns should win: got %q, %v; want %q", got, ok, store)
	}

	// No .devdns anywhere above an isolated directory.
	if got, ok := findProjectStore(t.TempDir()); ok {
		t.Errorf("findProjectStore of an empty tree = %q, true; want false", got)
	}
}

func TestResolveFrom(t *testing.T) {
	home := t.TempDir()

	// --dir wins and is used verbatim as the store.
	explicit := t.TempDir()
	a, err := resolveFrom(explicit, filepath.Join(t.TempDir(), "cwd"), home)
	if err != nil {
		t.Fatal(err)
	}
	if a.kind != kindExplicit || a.dir != explicit {
		t.Errorf("explicit: kind=%v dir=%q; want explicit %q", a.kind, a.dir, explicit)
	}
	if a.recordsPath != filepath.Join(explicit, "records.yaml") {
		t.Errorf("explicit recordsPath = %q", a.recordsPath)
	}

	// A project .devdns discovered by walking up from a subdirectory.
	proj := t.TempDir()
	store := filepath.Join(proj, ".devdns")
	sub := filepath.Join(proj, "sub")
	mustMkdirAll(t, store)
	mustMkdirAll(t, sub)
	if a, err = resolveFrom("", sub, home); err != nil {
		t.Fatal(err)
	} else if a.kind != kindProject || a.dir != store {
		t.Errorf("project: kind=%v dir=%q; want project %q", a.kind, a.dir, store)
	}

	// No project store -> the global home.
	if a, err = resolveFrom("", t.TempDir(), home); err != nil {
		t.Fatal(err)
	} else if a.kind != kindGlobal || a.dir != home {
		t.Errorf("fallback: kind=%v dir=%q; want global %q", a.kind, a.dir, home)
	}

	// The global home's own .devdns must not be mislabeled as a project store.
	homeParent := t.TempDir()
	realHome := filepath.Join(homeParent, ".devdns")
	work := filepath.Join(homeParent, "work")
	mustMkdirAll(t, realHome)
	mustMkdirAll(t, work)
	if a, err = resolveFrom("", work, realHome); err != nil {
		t.Fatal(err)
	} else if a.kind != kindGlobal || a.dir != realHome {
		t.Errorf("home guard: kind=%v dir=%q; want global %q", a.kind, a.dir, realHome)
	}
}

func mustMkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}
