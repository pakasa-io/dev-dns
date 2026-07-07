// Command devdns manages a local development DNS zone served by CoreDNS.
//
// records.yaml is the source of truth. Mutating commands (add/remove/update)
// rewrite it and regenerate the CoreDNS Corefile and zone file; a running
// CoreDNS picks the changes up automatically within a few seconds.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"local-dns/internal/coredns"
	"local-dns/internal/generator"
	"local-dns/internal/records"
	"local-dns/internal/validation"
)

const version = "0.1.0"

func main() {
	err := run(os.Args[1:])
	if err == nil || err == flag.ErrHelp {
		return
	}
	fmt.Fprintln(os.Stderr, "devdns: "+err.Error())
	os.Exit(1)
}

func run(args []string) error {
	if len(args) == 0 {
		usage(os.Stdout)
		return nil
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "list", "ls":
		return cmdList(rest)
	case "add":
		return cmdAdd(rest)
	case "update", "set":
		return cmdUpdate(rest)
	case "remove", "rm", "del":
		return cmdRemove(rest)
	case "validate":
		return cmdValidate(rest)
	case "generate", "gen":
		return cmdGenerate(rest)
	case "start":
		return cmdStart(rest)
	case "stop":
		return cmdStop(rest)
	case "restart":
		return cmdRestart(rest)
	case "reload":
		return cmdReload(rest)
	case "status":
		return cmdStatus(rest)
	case "install-coredns", "install":
		return cmdInstall(rest)
	case "version", "-v", "--version":
		fmt.Println("devdns " + version)
		return nil
	case "help", "-h", "--help":
		usage(os.Stdout)
		return nil
	default:
		return fmt.Errorf("unknown command %q (run `devdns help`)", cmd)
	}
}

// app holds the resolved paths derived from the records file location.
type app struct {
	recordsPath string
	root        string
	zonesDir    string
	corefile    string
	stateDir    string
}

func newApp(recordsPath string) (*app, error) {
	abs, err := filepath.Abs(recordsPath)
	if err != nil {
		return nil, err
	}
	root := filepath.Dir(abs)
	return &app{
		recordsPath: abs,
		root:        root,
		zonesDir:    filepath.Join(root, "zones"),
		corefile:    filepath.Join(root, "Corefile"),
		stateDir:    filepath.Join(root, ".devdns"),
	}, nil
}

func (a *app) manager(binary string) *coredns.Manager {
	return &coredns.Manager{
		Binary:   binary,
		Corefile: a.corefile,
		WorkDir:  a.root,
		StateDir: a.stateDir,
	}
}

// zoneRelPath is the zone file path as written into the Corefile (relative to
// the CoreDNS working directory, which is the project root).
func (a *app) zoneRelPath(zone string) string {
	return filepath.Join("zones", zone+".db")
}

// writeFiles renders and writes the zone file and Corefile. It assumes cfg has
// already been validated.
func (a *app) writeFiles(cfg *records.Config) error {
	if err := os.MkdirAll(a.zonesDir, 0o755); err != nil {
		return err
	}
	serial := uint32(time.Now().Unix())
	zoneRel := a.zoneRelPath(cfg.Zone)
	zonePath := filepath.Join(a.root, zoneRel)
	if err := os.WriteFile(zonePath, []byte(generator.Zone(cfg, serial)), 0o644); err != nil {
		return err
	}
	return os.WriteFile(a.corefile, []byte(generator.Corefile(cfg, zoneRel)), 0o644)
}

// regenerate validates cfg and rewrites the generated files.
func (a *app) regenerate(cfg *records.Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	return a.writeFiles(cfg)
}

// persist validates cfg, saves records.yaml, and regenerates the output files.
func (a *app) persist(cfg *records.Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := records.Save(a.recordsPath, cfg); err != nil {
		return err
	}
	return a.writeFiles(cfg)
}

// reloadNotice tells the user whether a running CoreDNS will apply the change.
func (a *app) reloadNotice() {
	if a.manager("").Status().Running {
		fmt.Println("CoreDNS is running; it will apply the change within ~5s.")
	}
}

// ---- flag helpers ----

func newFlagSet(name, usageLine string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, usageLine)
		fmt.Fprintln(os.Stderr, "\nFlags:")
		fs.PrintDefaults()
	}
	return fs
}

func fileFlag(fs *flag.FlagSet) *string {
	return fs.String("file", "records.yaml", "path to the records file")
}

// parseMixed parses fs while allowing flags to appear before, after, or
// interleaved with positional arguments. The stdlib flag package stops at the
// first positional, so we parse repeatedly, peeling off one positional at a
// time. It returns the positional arguments in order.
func parseMixed(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return positional, nil
		}
		positional = append(positional, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

// downloadAllowed reports whether start/restart may auto-download CoreDNS.
func downloadAllowed(noDownload bool) bool {
	return !noDownload && os.Getenv("DEVDNS_NO_DOWNLOAD") == ""
}

func logStderr(s string) { fmt.Fprintln(os.Stderr, s) }

// ---- record commands ----

func cmdList(args []string) error {
	fs := newFlagSet("list", "usage: devdns list [--file F] [--json]")
	file := fileFlag(fs)
	asJSON := fs.Bool("json", false, "output records as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	a, err := newApp(*file)
	if err != nil {
		return err
	}
	cfg, err := records.Load(a.recordsPath)
	if err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(cfg)
	}
	if len(cfg.Records) == 0 {
		fmt.Printf("No records in zone %s.\n", cfg.Zone)
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tTYPE\tVALUE\tTTL\tFQDN")
	for _, r := range cfg.Records {
		ttl := "-"
		if r.TTL > 0 {
			ttl = strconv.Itoa(r.TTL)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", r.Name, r.Type, r.Value, ttl, records.FQDN(r.Name, cfg.Zone))
	}
	return w.Flush()
}

func cmdAdd(args []string) error {
	fs := newFlagSet("add", "usage: devdns add <name> <value> [--type A|AAAA|CNAME] [--ttl N] [--file F]")
	file := fileFlag(fs)
	typ := fs.String("type", "", "record type; inferred from the value when empty")
	ttl := fs.Int("ttl", 0, "per-record TTL in seconds (0 = use the zone default)")
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 2 {
		fs.Usage()
		return fmt.Errorf("expected <name> and <value>")
	}
	a, cfg, err := load(*file)
	if err != nil {
		return err
	}
	rec, err := buildRecord(cfg, pos[0], pos[1], *typ, *ttl)
	if err != nil {
		return err
	}
	added, err := cfg.Add(rec)
	if err != nil {
		return err
	}
	if !added {
		fmt.Printf("%s %s %s is already present; nothing to do.\n", records.Display(rec.Name, cfg.Zone), rec.Type, rec.Value)
		return nil
	}
	if err := a.persist(cfg); err != nil {
		return err
	}
	fmt.Printf("Added %s %s %s\n", records.Display(rec.Name, cfg.Zone), rec.Type, rec.Value)
	a.reloadNotice()
	return nil
}

func cmdUpdate(args []string) error {
	fs := newFlagSet("update", "usage: devdns update <name> <value> [--type A|AAAA|CNAME] [--ttl N] [--file F]")
	file := fileFlag(fs)
	typ := fs.String("type", "", "record type; inferred from the value when empty")
	ttl := fs.Int("ttl", 0, "per-record TTL in seconds (0 = use the zone default)")
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 2 {
		fs.Usage()
		return fmt.Errorf("expected <name> and <value>")
	}
	a, cfg, err := load(*file)
	if err != nil {
		return err
	}
	rec, err := buildRecord(cfg, pos[0], pos[1], *typ, *ttl)
	if err != nil {
		return err
	}
	created, err := cfg.Update(rec)
	if err != nil {
		return err
	}
	if err := a.persist(cfg); err != nil {
		return err
	}
	verb := "Updated"
	if created {
		verb = "Added"
	}
	fmt.Printf("%s %s %s %s\n", verb, records.Display(rec.Name, cfg.Zone), rec.Type, rec.Value)
	a.reloadNotice()
	return nil
}

func cmdRemove(args []string) error {
	fs := newFlagSet("remove", "usage: devdns remove <name> [--type A|AAAA|CNAME] [--file F]")
	file := fileFlag(fs)
	typ := fs.String("type", "", "only remove records of this type")
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		fs.Usage()
		return fmt.Errorf("expected a single <name>")
	}
	a, cfg, err := load(*file)
	if err != nil {
		return err
	}
	name, err := records.NormalizeName(pos[0], cfg.Zone)
	if err != nil {
		return err
	}
	rt := ""
	if *typ != "" {
		if rt, err = validation.NormalizeType(*typ); err != nil {
			return err
		}
	}
	n := cfg.Remove(name, rt)
	if n == 0 {
		return fmt.Errorf("no matching record for %s", records.Display(name, cfg.Zone))
	}
	if err := a.persist(cfg); err != nil {
		return err
	}
	fmt.Printf("Removed %d record(s) for %s\n", n, records.Display(name, cfg.Zone))
	a.reloadNotice()
	return nil
}

func cmdValidate(args []string) error {
	fs := newFlagSet("validate", "usage: devdns validate [--file F]")
	file := fileFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	_, cfg, err := load(*file)
	if err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	fmt.Printf("OK: zone %s, %d record(s)\n", cfg.Zone, len(cfg.Records))
	return nil
}

// ---- server commands ----

func cmdGenerate(args []string) error {
	fs := newFlagSet("generate", "usage: devdns generate [--file F]")
	file := fileFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	a, cfg, err := load(*file)
	if err != nil {
		return err
	}
	if err := a.regenerate(cfg); err != nil {
		return err
	}
	fmt.Printf("Generated %s and %s\n", rel(a.root, a.corefile), a.zoneRelPath(cfg.Zone))
	a.reloadNotice()
	return nil
}

func cmdStart(args []string) error {
	fs := newFlagSet("start", "usage: devdns start [--file F] [--coredns PATH] [--no-download]")
	file := fileFlag(fs)
	binary := fs.String("coredns", "", "path to the coredns binary")
	noDownload := fs.Bool("no-download", false, "do not auto-download CoreDNS if it is missing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	a, cfg, err := load(*file)
	if err != nil {
		return err
	}
	if err := a.regenerate(cfg); err != nil {
		return err
	}
	bin, err := coredns.EnsureBinary(*binary, a.root, downloadAllowed(*noDownload), logStderr)
	if err != nil {
		return err
	}
	msg, err := a.manager(bin).Start()
	if err != nil {
		if cfg.ResolvedPort() < 1024 {
			return fmt.Errorf("%w\n\nport %d requires elevated privileges: either run with sudo, "+
				"or set `port: 1053` in %s and run `devdns start` again",
				err, cfg.ResolvedPort(), filepath.Base(a.recordsPath))
		}
		return err
	}
	fmt.Println(msg)
	fmt.Printf("Serving zone %s on %s:%d; forwarding everything else to %s\n",
		cfg.Zone, listenAddr(cfg), cfg.ResolvedPort(), strings.Join(cfg.ResolvedUpstreams(), ", "))
	return nil
}

func cmdStop(args []string) error {
	fs := newFlagSet("stop", "usage: devdns stop [--file F]")
	file := fileFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	a, err := newApp(*file)
	if err != nil {
		return err
	}
	msg, err := a.manager("").Stop()
	if err != nil {
		return err
	}
	fmt.Println(msg)
	return nil
}

func cmdRestart(args []string) error {
	fs := newFlagSet("restart", "usage: devdns restart [--file F] [--coredns PATH] [--no-download]")
	file := fileFlag(fs)
	binary := fs.String("coredns", "", "path to the coredns binary")
	noDownload := fs.Bool("no-download", false, "do not auto-download CoreDNS if it is missing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	a, cfg, err := load(*file)
	if err != nil {
		return err
	}
	if err := a.regenerate(cfg); err != nil {
		return err
	}
	bin, err := coredns.EnsureBinary(*binary, a.root, downloadAllowed(*noDownload), logStderr)
	if err != nil {
		return err
	}
	msg, err := a.manager(bin).Restart()
	if err != nil {
		return err
	}
	fmt.Println(msg)
	return nil
}

func cmdReload(args []string) error {
	fs := newFlagSet("reload", "usage: devdns reload [--file F]")
	file := fileFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	a, cfg, err := load(*file)
	if err != nil {
		return err
	}
	if err := a.regenerate(cfg); err != nil {
		return err
	}
	if a.manager("").Status().Running {
		fmt.Println("Regenerated config; CoreDNS will reload within ~5s.")
	} else {
		fmt.Println("Regenerated config; CoreDNS is not running (start it with `devdns start`).")
	}
	return nil
}

func cmdStatus(args []string) error {
	fs := newFlagSet("status", "usage: devdns status [--file F]")
	file := fileFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	a, err := newApp(*file)
	if err != nil {
		return err
	}
	st := a.manager("").Status()
	if st.Running {
		fmt.Printf("CoreDNS:  running (pid %d)\n", st.PID)
	} else {
		fmt.Println("CoreDNS:  stopped")
	}
	if cfg, err := records.Load(a.recordsPath); err == nil {
		fmt.Printf("Zone:     %s\n", cfg.Zone)
		fmt.Printf("Listen:   %s:%d\n", listenAddr(cfg), cfg.ResolvedPort())
		fmt.Printf("Upstream: %s\n", strings.Join(cfg.ResolvedUpstreams(), ", "))
		fmt.Printf("Records:  %d\n", len(cfg.Records))
	}
	fmt.Printf("Corefile: %s\n", a.corefile)
	return nil
}

func cmdInstall(args []string) error {
	fs := newFlagSet("install-coredns", "usage: devdns install-coredns [--file F] [--coredns-version V]")
	file := fileFlag(fs)
	ver := fs.String("coredns-version", "", "CoreDNS version to download (default "+coredns.DefaultVersion+")")
	if err := fs.Parse(args); err != nil {
		return err
	}
	a, err := newApp(*file)
	if err != nil {
		return err
	}
	binPath, err := coredns.Install(coredns.InstallOptions{
		Version: *ver,
		Dest:    coredns.DefaultDest(a.root),
		Log:     logStderr,
	})
	if err != nil {
		return err
	}
	fmt.Println(binPath)
	return nil
}

// ---- shared helpers ----

// load resolves paths and loads the records file in one step.
func load(file string) (*app, *records.Config, error) {
	a, err := newApp(file)
	if err != nil {
		return nil, nil, err
	}
	cfg, err := records.Load(a.recordsPath)
	if err != nil {
		return nil, nil, err
	}
	return a, cfg, nil
}

func buildRecord(cfg *records.Config, name, value, typ string, ttl int) (records.Record, error) {
	nn, err := records.NormalizeName(name, cfg.Zone)
	if err != nil {
		return records.Record{}, err
	}
	value = strings.TrimSpace(value)
	var rt string
	if typ == "" {
		if rt, err = inferType(value); err != nil {
			return records.Record{}, err
		}
	} else if rt, err = validation.NormalizeType(typ); err != nil {
		return records.Record{}, err
	}
	return records.Record{Name: nn, Type: rt, Value: value, TTL: ttl}, nil
}

// inferType guesses the record type from the value: a valid IPv4 -> A, a valid
// IPv6 -> AAAA, otherwise CNAME. A value that clearly looks like an IP address
// but does not parse is rejected, so a typo'd address is not silently turned
// into a CNAME. Pass --type to force a specific type.
func inferType(value string) (string, error) {
	switch {
	case validation.ValidIPv4(value):
		return "A", nil
	case validation.ValidIPv6(value):
		return "AAAA", nil
	case looksLikeIP(value):
		return "", fmt.Errorf("%q looks like an IP address but is not valid; "+
			"fix it, or pass --type to force a record type", value)
	default:
		return "CNAME", nil
	}
}

// looksLikeIP reports whether value appears to be an intended IP address:
// only digits and dots (IPv4-ish), or containing a colon (IPv6-ish).
func looksLikeIP(value string) bool {
	if strings.Contains(value, ":") {
		return true
	}
	if !strings.Contains(value, ".") {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && r != '.' {
			return false
		}
	}
	return true
}

func listenAddr(cfg *records.Config) string {
	if a := strings.TrimSpace(cfg.Address); a != "" {
		return a
	}
	return "0.0.0.0"
}

func rel(base, path string) string {
	if r, err := filepath.Rel(base, path); err == nil {
		return r
	}
	return path
}

func usage(w *os.File) {
	fmt.Fprint(w, `devdns -- manage a local development DNS zone backed by CoreDNS.

Usage:
  devdns <command> [flags]

Record commands:
  list                    List records (--json for machine-readable output)
  add <name> <value>      Add a record; type is inferred from the value
  update <name> <value>   Add or replace a record
  remove <name>           Remove records for a name (--type to narrow)
  validate                Validate records.yaml

Server commands:
  generate                Regenerate the Corefile and zone file
  start                   Start CoreDNS in the background
  stop                    Stop CoreDNS
  restart                 Restart CoreDNS
  reload                  Regenerate config (a running CoreDNS reloads itself)
  status                  Show CoreDNS status and configuration
  install-coredns         Download CoreDNS into ./bin (auto-runs at start)

Other:
  version                 Print the devdns version
  help                    Show this help

Every command accepts --file <path> to point at a records file (default records.yaml).

Examples:
  devdns add app 127.0.0.1
  devdns add api.example.internal 127.0.0.1
  devdns add www app --type CNAME
  devdns remove api
  devdns generate && devdns start
  dig @127.0.0.1 -p 1053 app.example.internal
`)
}
