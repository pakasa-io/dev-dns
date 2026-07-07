package generator

import (
	"strings"
	"testing"

	"local-dns/internal/records"
)

func sampleConfig() *records.Config {
	return &records.Config{
		Zone: "example.internal",
		Records: []records.Record{
			{Name: "app", Type: "A", Value: "127.0.0.1"},
			{Name: "v6", Type: "AAAA", Value: "::1"},
			{Name: "www", Type: "CNAME", Value: "app"},
			{Name: "ext", Type: "CNAME", Value: "example.com"},
		},
	}
}

func TestZone(t *testing.T) {
	out := Zone(sampleConfig(), 20260707)
	wants := []string{
		"$ORIGIN example.internal.",
		"$TTL 60",
		"IN\tSOA\tns.example.internal. admin.example.internal.",
		"20260707\t; serial",
		"@\tIN\tNS\tns.example.internal.",
		"ns\tIN\tA\t127.0.0.1",
		"app\tIN\tA\t127.0.0.1",
		"v6\tIN\tAAAA\t::1",
		"www\tIN\tCNAME\tapp\n",          // in-zone target stays relative
		"ext\tIN\tCNAME\texample.com.\n", // external target is fully qualified
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("zone output missing %q\n---\n%s", w, out)
		}
	}
}

func TestZoneSynthesizesNSGlueOnlyWhenAbsent(t *testing.T) {
	cfg := &records.Config{
		Zone:    "example.internal",
		Records: []records.Record{{Name: "ns", Type: "A", Value: "10.0.0.53"}},
	}
	out := Zone(cfg, 1)
	if strings.Contains(out, "ns\tIN\tA\t127.0.0.1") {
		t.Errorf("should not synthesize ns glue when user defines ns:\n%s", out)
	}
	if !strings.Contains(out, "ns\tIN\tA\t10.0.0.53") {
		t.Errorf("user ns record missing:\n%s", out)
	}
}

func TestZoneRespectsPerRecordTTL(t *testing.T) {
	cfg := &records.Config{
		Zone:    "example.internal",
		Records: []records.Record{{Name: "app", Type: "A", Value: "127.0.0.1", TTL: 300}},
	}
	if out := Zone(cfg, 1); !strings.Contains(out, "app\t300\tIN\tA\t127.0.0.1") {
		t.Errorf("per-record TTL missing:\n%s", out)
	}
}

func TestCorefile(t *testing.T) {
	cfg := sampleConfig()
	cfg.Port = 1053
	out := Corefile(cfg, "zones/example.internal.db")
	wants := []string{
		"example.internal:1053 {",
		"file zones/example.internal.db {",
		"reload 5s",
		".:1053 {",
		"forward . 1.1.1.1 1.0.0.1 8.8.8.8 8.8.4.4",
		"cache 30",
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("Corefile missing %q\n---\n%s", w, out)
		}
	}
	if strings.Contains(out, "bind ") {
		t.Errorf("did not expect a bind directive when address is unset:\n%s", out)
	}
}

func TestCorefileBindAndUpstreams(t *testing.T) {
	cfg := &records.Config{Zone: "example.internal", Address: "127.0.0.1", Upstreams: []string{"9.9.9.9"}}
	out := Corefile(cfg, "zones/example.internal.db")
	if !strings.Contains(out, "bind 127.0.0.1") {
		t.Errorf("expected bind directive:\n%s", out)
	}
	if !strings.Contains(out, "forward . 9.9.9.9\n") {
		t.Errorf("expected custom upstream:\n%s", out)
	}
}
