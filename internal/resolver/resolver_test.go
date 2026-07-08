package resolver

import (
	"strings"
	"testing"
)

func TestMacOSContent(t *testing.T) {
	// High port includes the port directive.
	got := macOSContent(Route{Zone: "nyange.internal", Port: 1053})
	want := "nameserver 127.0.0.1\nport 1053\n"
	if got != want {
		t.Errorf("high port content = %q; want %q", got, want)
	}
	// Port 53 (the OS default) omits the port line.
	if got := macOSContent(Route{Zone: "nyange.internal", Port: 53}); got != "nameserver 127.0.0.1\n" {
		t.Errorf("port 53 content = %q; want no port line", got)
	}
	// A custom address is honored.
	if got := macOSContent(Route{Zone: "z", Addr: "127.0.0.53", Port: 1053}); !strings.Contains(got, "nameserver 127.0.0.53") {
		t.Errorf("custom addr not honored: %q", got)
	}
}

func TestMacOSPlanPaths(t *testing.T) {
	in := macOSPlan(Route{Zone: "nyange.internal", Port: 1053}, false)
	if in.Path != "/etc/resolver/nyange.internal" {
		t.Errorf("install path = %q", in.Path)
	}
	if in.Remove || !in.Supported || in.Content == "" {
		t.Errorf("install plan wrong: %+v", in)
	}
	rm := macOSPlan(Route{Zone: "nyange.internal", Port: 1053}, true)
	if !rm.Remove || rm.Path != in.Path {
		t.Errorf("uninstall plan wrong: %+v", rm)
	}
}

func TestSystemdContent(t *testing.T) {
	got := systemdContent(Route{Zone: "nyange.internal", Port: 1053})
	for _, sub := range []string{"[Resolve]", "DNS=127.0.0.1:1053", "Domains=~nyange.internal"} {
		if !strings.Contains(got, sub) {
			t.Errorf("systemd content missing %q in:\n%s", sub, got)
		}
	}
	if p := systemdConfPath("nyange.internal"); p != "/etc/systemd/resolved.conf.d/devdns-nyange.internal.conf" {
		t.Errorf("systemd path = %q", p)
	}
}

func TestManualAlwaysPresent(t *testing.T) {
	// Every plan (supported or not) must carry human-readable steps for preview.
	for _, p := range []Plan{
		macOSPlan(Route{Zone: "z", Port: 1053}, false),
		systemdPlan(Route{Zone: "z", Port: 1053}, false),
		unsupportedPlan(Route{Zone: "z", Port: 1053}, false, genericManual(Route{Zone: "z", Port: 1053}, false)),
	} {
		if len(p.Manual) == 0 {
			t.Errorf("%s plan has no Manual steps", p.Backend)
		}
	}
}
