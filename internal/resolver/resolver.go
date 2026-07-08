// Package resolver installs and removes the per-domain OS DNS route that sends a
// devdns zone to the local CoreDNS instance (e.g. macOS /etc/resolver or a
// systemd-resolved drop-in).
//
// Building the plan is pure and OS-aware; applying it writes system files and
// therefore needs root, so callers elevate only that step. Everything else --
// generating the store and running CoreDNS on a high port -- stays unprivileged.
package resolver

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// Route is the per-domain routing devdns manages: send Zone to Addr:Port.
type Route struct {
	Zone string
	Addr string // defaults to 127.0.0.1 when empty
	Port int
}

func (r Route) addr() string {
	if a := strings.TrimSpace(r.Addr); a != "" {
		return a
	}
	return "127.0.0.1"
}

// Plan is the concrete set of changes for a route. It doubles as a human-facing
// preview (Manual) and as the instructions Apply executes.
type Plan struct {
	Backend   string     // human name, e.g. "macOS /etc/resolver"
	Supported bool       // false => this OS has no automated backend; use Manual
	Path      string     // the single file devdns manages for this route
	Content   string     // desired file content (ignored when Remove)
	Remove    bool       // true => delete Path instead of writing it
	Post      [][]string // commands to run after the file change (need root)
	Manual    []string   // copy-pasteable equivalent steps (preview / fallback)
}

// InstallPlan returns the OS-appropriate plan to route r to the local resolver.
func InstallPlan(r Route) Plan { return planFor(r, false) }

// UninstallPlan returns the plan to remove the route for r.
func UninstallPlan(r Route) Plan { return planFor(r, true) }

func planFor(r Route, remove bool) Plan {
	switch runtime.GOOS {
	case "darwin":
		return macOSPlan(r, remove)
	case "linux":
		if systemdResolvedActive() {
			return systemdPlan(r, remove)
		}
		return unsupportedPlan(r, remove, linuxManual(r, remove))
	default:
		return unsupportedPlan(r, remove, genericManual(r, remove))
	}
}

// ---- macOS: /etc/resolver/<zone> ----

func macOSPlan(r Route, remove bool) Plan {
	path := filepath.Join("/etc/resolver", r.Zone)
	flush := [][]string{{"dscacheutil", "-flushcache"}, {"killall", "-HUP", "mDNSResponder"}}
	p := Plan{Backend: "macOS /etc/resolver", Supported: true, Path: path, Post: flush}
	if remove {
		p.Remove = true
		p.Manual = []string{
			fmt.Sprintf("sudo rm %s", path),
			"sudo dscacheutil -flushcache; sudo killall -HUP mDNSResponder",
		}
		return p
	}
	p.Content = macOSContent(r)
	p.Manual = []string{
		"sudo mkdir -p /etc/resolver",
		fmt.Sprintf("sudo tee %s >/dev/null <<'EOF'\n%sEOF", path, p.Content),
		"sudo dscacheutil -flushcache; sudo killall -HUP mDNSResponder",
	}
	return p
}

func macOSContent(r Route) string {
	var b strings.Builder
	fmt.Fprintf(&b, "nameserver %s\n", r.addr())
	if r.Port != 0 && r.Port != 53 {
		fmt.Fprintf(&b, "port %d\n", r.Port)
	}
	return b.String()
}

// ---- Linux: systemd-resolved drop-in ----

func systemdConfPath(zone string) string {
	return filepath.Join("/etc/systemd/resolved.conf.d", "devdns-"+zone+".conf")
}

func systemdPlan(r Route, remove bool) Plan {
	path := systemdConfPath(r.Zone)
	restart := [][]string{{"systemctl", "restart", "systemd-resolved"}}
	p := Plan{Backend: "systemd-resolved drop-in", Supported: true, Path: path, Post: restart}
	if remove {
		p.Remove = true
		p.Manual = []string{
			fmt.Sprintf("sudo rm %s", path),
			"sudo systemctl restart systemd-resolved",
		}
		return p
	}
	p.Content = systemdContent(r)
	p.Manual = []string{
		"sudo mkdir -p /etc/systemd/resolved.conf.d",
		fmt.Sprintf("sudo tee %s >/dev/null <<'EOF'\n%sEOF", path, p.Content),
		"sudo systemctl restart systemd-resolved",
	}
	return p
}

func systemdContent(r Route) string {
	port := r.Port
	if port == 0 {
		port = 53
	}
	var b strings.Builder
	b.WriteString("# Managed by devdns.\n[Resolve]\n")
	fmt.Fprintf(&b, "DNS=%s:%d\n", r.addr(), port)
	fmt.Fprintf(&b, "Domains=~%s\n", r.Zone)
	return b.String()
}

// systemdResolvedActive reports whether systemd-resolved is the active resolver,
// so the drop-in backend will actually take effect.
func systemdResolvedActive() bool {
	if _, err := os.Stat("/run/systemd/resolve/stub-resolv.conf"); err == nil {
		return true
	}
	if out, err := exec.Command("systemctl", "is-active", "systemd-resolved").Output(); err == nil {
		return strings.TrimSpace(string(out)) == "active"
	}
	return false
}

// ---- Unsupported: describe the manual steps ----

func unsupportedPlan(r Route, remove bool, manual []string) Plan {
	return Plan{Backend: runtime.GOOS + " (no automated backend)", Supported: false, Manual: manual}
}

func linuxManual(r Route, remove bool) []string {
	if remove {
		return []string{"Remove whatever per-domain DNS routing you configured for " + r.Zone + " (dnsmasq, NetworkManager, /etc/resolv.conf, ...)."}
	}
	return []string{
		"systemd-resolved is not active. Route " + r.Zone + " to " + r.addr() + fmt.Sprintf(":%d", portOr53(r.Port)) + " using your resolver:",
		"  - dnsmasq:   server=/" + r.Zone + "/" + r.addr() + "#" + strconv.Itoa(portOr53(r.Port)),
		"  - or point your stub resolver / NetworkManager at " + r.addr() + " for " + r.Zone + ".",
	}
}

func genericManual(r Route, remove bool) []string {
	if remove {
		return []string{"Remove the DNS routing rule you added for " + r.Zone + "."}
	}
	if runtime.GOOS == "windows" {
		return []string{
			"Windows can route a suffix to a nameserver via NRPT, but not to a custom port,",
			"so use port 53 (re-run `devdns init --port 53`) and, in an elevated PowerShell:",
			fmt.Sprintf("  Add-DnsClientNrptRule -Namespace \".%s\" -NameServers \"%s\"", r.Zone, r.addr()),
		}
	}
	return []string{fmt.Sprintf("Route %s to %s:%d using your OS's per-domain DNS mechanism.", r.Zone, r.addr(), portOr53(r.Port))}
}

func portOr53(p int) int {
	if p == 0 {
		return 53
	}
	return p
}

// Apply performs the plan's file change and post-commands. It must run as root
// (the caller elevates just this step). It is a no-op for unsupported plans.
func Apply(p Plan) error {
	if !p.Supported {
		return fmt.Errorf("no automated resolver backend for this OS; apply the manual steps")
	}
	if p.Remove {
		if err := os.Remove(p.Path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing %s: %w", p.Path, err)
		}
	} else {
		if err := os.MkdirAll(filepath.Dir(p.Path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p.Path, []byte(p.Content), 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", p.Path, err)
		}
	}
	for _, cmd := range p.Post {
		// Best-effort: a failed cache flush should not fail the whole install.
		_ = exec.Command(cmd[0], cmd[1:]...).Run()
	}
	return nil
}

// Installed reports whether the route file for r currently exists, and its path.
// Reading is unprivileged, so `status` needs no elevation.
func Installed(r Route) (bool, string) {
	p := InstallPlan(r)
	if p.Path == "" {
		return false, ""
	}
	_, err := os.Stat(p.Path)
	return err == nil, p.Path
}
