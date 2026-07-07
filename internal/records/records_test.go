package records

import (
	"os"
	"path/filepath"
	"testing"
)

func baseConfig() *Config {
	return &Config{
		Zone: "example.internal",
		Records: []Record{
			{Name: "app", Type: "A", Value: "127.0.0.1"},
		},
	}
}

func TestNormalizeName(t *testing.T) {
	zone := "example.internal"
	cases := map[string]string{
		"app":                   "app",
		"app.example.internal":  "app",
		"app.example.internal.": "app",
		"APP":                   "app",
		"@":                     "@",
		"example.internal":      "@",
		"a.b":                   "a.b",
		"a.b.example.internal":  "a.b",
		"*.example.internal":    "*",
	}
	for in, want := range cases {
		got, err := NormalizeName(in, zone)
		if err != nil || got != want {
			t.Errorf("NormalizeName(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := NormalizeName("   ", zone); err == nil {
		t.Errorf("NormalizeName(blank) = nil error; want error")
	}
}

func TestAddIdempotentAndDuplicate(t *testing.T) {
	c := baseConfig()
	// Identical add is a no-op.
	added, err := c.Add(Record{Name: "app", Type: "A", Value: "127.0.0.1"})
	if err != nil || added {
		t.Fatalf("idempotent add: added=%v err=%v; want false, nil", added, err)
	}
	// Same name/type, different value is a conflict.
	if _, err := c.Add(Record{Name: "app", Type: "A", Value: "127.0.0.2"}); err == nil {
		t.Fatalf("duplicate add: want error, got nil")
	}
	// New name is fine.
	added, err = c.Add(Record{Name: "api", Type: "A", Value: "127.0.0.1"})
	if err != nil || !added {
		t.Fatalf("new add: added=%v err=%v; want true, nil", added, err)
	}
}

func TestCNAMEConflict(t *testing.T) {
	c := baseConfig()
	if _, err := c.Add(Record{Name: "app", Type: "CNAME", Value: "api"}); err == nil {
		t.Errorf("CNAME over existing A: want error, got nil")
	}
	c2 := &Config{Zone: "example.internal"}
	if _, err := c2.Add(Record{Name: "www", Type: "CNAME", Value: "app"}); err != nil {
		t.Fatalf("first CNAME add: %v", err)
	}
	if _, err := c2.Add(Record{Name: "www", Type: "A", Value: "127.0.0.1"}); err == nil {
		t.Errorf("A over existing CNAME: want error, got nil")
	}
}

func TestUpdateAndRemove(t *testing.T) {
	c := baseConfig()
	created, err := c.Update(Record{Name: "app", Type: "A", Value: "10.0.0.1"})
	if err != nil || created {
		t.Fatalf("update existing: created=%v err=%v; want false, nil", created, err)
	}
	if c.Records[0].Value != "10.0.0.1" {
		t.Fatalf("update value = %q; want 10.0.0.1", c.Records[0].Value)
	}
	if n := c.Remove("app", "AAAA"); n != 0 {
		t.Fatalf("remove wrong type removed %d; want 0", n)
	}
	if n := c.Remove("app", ""); n != 1 {
		t.Fatalf("remove app removed %d; want 1", n)
	}
	if len(c.Records) != 0 {
		t.Fatalf("records left = %d; want 0", len(c.Records))
	}
}

func TestValidateRejectsBadRecords(t *testing.T) {
	bad := []*Config{
		{Zone: "", Records: nil},
		{Zone: "example.internal", Records: []Record{{Name: "x", Type: "A", Value: "999.0.0.1"}}},
		{Zone: "example.internal", Records: []Record{{Name: "x", Type: "MX", Value: "127.0.0.1"}}},
		{Zone: "example.internal", Records: []Record{
			{Name: "x", Type: "A", Value: "127.0.0.1"},
			{Name: "x", Type: "A", Value: "127.0.0.2"},
		}},
	}
	for i, c := range bad {
		if err := c.Validate(); err == nil {
			t.Errorf("case %d: Validate() = nil; want error", i)
		}
	}
	if err := baseConfig().Validate(); err != nil {
		t.Errorf("valid config: Validate() = %v; want nil", err)
	}
}

func TestLoadNormalizesNames(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "records.yaml")
	content := "zone: Example.Internal.\nrecords:\n  - name: App.example.internal\n    type: a\n    value: 127.0.0.1\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Zone != "example.internal" {
		t.Errorf("zone = %q; want example.internal", c.Zone)
	}
	if c.Records[0].Name != "app" || c.Records[0].Type != "A" {
		t.Errorf("record normalized to %q/%q; want app/A", c.Records[0].Name, c.Records[0].Type)
	}
	if err := Save(path, c); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("reload after save: %v", err)
	}
}
