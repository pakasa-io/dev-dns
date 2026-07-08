// Command devdns manages a local development DNS zone served by CoreDNS.
//
// devdns keeps everything for a zone in a store directory: a project-local
// ./.devdns (found by walking up from the current directory, git-style) takes
// precedence over the global ~/.devdns. records.yaml inside the store is the
// source of truth; mutating commands (add/remove/update) rewrite it and
// regenerate the CoreDNS Corefile and zone file, which a running CoreDNS picks
// up automatically within a few seconds.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"dev-dns/internal/coredns"
	"dev-dns/internal/generator"
	"dev-dns/internal/records"
	"dev-dns/internal/validation"
)

// version is set at build time via -ldflags "-X main.version=...".
// It defaults to "dev" for plain `go build`; `make build` injects git describe,
// and GoReleaser injects the release tag.
var version = "dev"

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
	case "init":
		return cmdInit(rest)
	case "where":
		return cmdWhere(rest)
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

// manager builds a CoreDNS process manager for this store. CoreDNS runs in the
// store dir so the relative zone paths resolve, and the pidfile/log live there
// too.
func (a *app) manager(binary string) *coredns.Manager {
	return &coredns.Manager{
		Binary:   binary,
		Corefile: a.corefile,
		WorkDir:  a.dir,
		StateDir: a.dir,
	}
}

// zoneRelPath is the zone file path as written into the Corefile (relative to
// the CoreDNS working directory, which is the store dir).
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
	zonePath := filepath.Join(a.dir, zoneRel)
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

// ---- store resolution helpers ----

// openStore resolves the active store (see resolve) without loading its records.
func openStore(dir string) (*app, error) {
	return resolve(dir)
}

// openConfig resolves the active store and loads its records, auto-initializing
// the global store the first time it is used.
func openConfig(dir string) (*app, *records.Config, error) {
	a, err := resolve(dir)
	if err != nil {
		return nil, nil, err
	}
	if err := a.ensureGlobalInitialized(); err != nil {
		return nil, nil, err
	}
	cfg, err := records.Load(a.recordsPath)
	if err != nil {
		return nil, nil, err
	}
	return a, cfg, nil
}

// ensureGlobalInitialized scaffolds a default records.yaml (and its generated
// files) the first time the global ~/.devdns store is used, so devdns works out
// of the box after installation. Project and explicit stores are created by
// `devdns init`.
func (a *app) ensureGlobalInitialized() error {
	if a.kind != kindGlobal {
		return nil
	}
	if _, err := os.Stat(a.recordsPath); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := writeDefaultRecords(a.recordsPath, defaultInitZone, defaultInitPort); err != nil {
		return err
	}
	logStderr("Initialized global devdns store at " + a.dir)
	cfg, err := records.Load(a.recordsPath)
	if err != nil {
		return err
	}
	return a.regenerate(cfg)
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

func addStoreFlag(fs *flag.FlagSet) *string {
	return fs.String("dir", "", "devdns store directory (default: nearest ./.devdns, else ~/.devdns)")
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

// ---- store commands ----

func cmdInit(args []string) error {
	fs := newFlagSet("init", "usage: devdns init [--global] [--dir PATH] [--zone Z] [--port N] [--force]")
	global := fs.Bool("global", false, "initialize the global ~/.devdns store instead of a project ./.devdns")
	dir := fs.String("dir", "", "explicit store directory to create (overrides --global and the default ./.devdns)")
	zone := fs.String("zone", defaultInitZone, "the authoritative local zone")
	port := fs.Int("port", defaultInitPort, "listen port")
	force := fs.Bool("force", false, "overwrite an existing store")
	if err := fs.Parse(args); err != nil {
		return err
	}
	target, err := initTarget(*global, *dir)
	if err != nil {
		return err
	}
	z := strings.ToLower(strings.TrimSpace(*zone))
	if err := validation.ValidateHostname(z); err != nil {
		return fmt.Errorf("invalid zone %q: %w", *zone, err)
	}
	recordsPath := filepath.Join(target, "records.yaml")
	if _, err := os.Stat(recordsPath); err == nil && !*force {
		return fmt.Errorf("a devdns store already exists at %s (use --force to overwrite)", target)
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := writeDefaultRecords(recordsPath, z, *port); err != nil {
		return err
	}
	home, err := globalHome()
	if err != nil {
		return err
	}
	kind := kindProject
	if target == home {
		kind = kindGlobal
	}
	a := newApp(target, home, kind)
	cfg, err := records.Load(a.recordsPath)
	if err != nil {
		return err
	}
	if err := a.regenerate(cfg); err != nil {
		return err
	}
	fmt.Printf("Initialized devdns store at %s (zone %s, port %d)\n", target, cfg.Zone, cfg.ResolvedPort())
	start := "devdns start"
	if cfg.ResolvedPort() < 1024 {
		start = "sudo devdns start"
	}
	fmt.Printf("Next: `devdns add <name> <value>`, then `%s`.\n", start)
	return nil
}

// initTarget picks the directory `init` should create: --dir if given, else
// ~/.devdns with --global, else ./.devdns in the current directory.
func initTarget(global bool, dir string) (string, error) {
	switch {
	case strings.TrimSpace(dir) != "":
		return filepath.Abs(dir)
	case global:
		return globalHome()
	default:
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		return filepath.Join(cwd, ".devdns"), nil
	}
}

func cmdWhere(args []string) error {
	fs := newFlagSet("where", "usage: devdns where [--dir PATH]")
	dir := addStoreFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	a, err := openStore(*dir)
	if err != nil {
		return err
	}
	fmt.Printf("%s (%s)\n", a.dir, a.kind)
	fmt.Printf("records: %s\n", a.recordsPath)
	return nil
}

// ---- record commands ----

func cmdList(args []string) error {
	fs := newFlagSet("list", "usage: devdns list [--dir PATH] [--json]")
	dir := addStoreFlag(fs)
	asJSON := fs.Bool("json", false, "output records as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	_, cfg, err := openConfig(*dir)
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
	fs := newFlagSet("add", "usage: devdns add <name> <value> [--type A|AAAA|CNAME] [--ttl N] [--dir PATH]")
	dir := addStoreFlag(fs)
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
	a, cfg, err := openConfig(*dir)
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
	fs := newFlagSet("update", "usage: devdns update <name> <value> [--type A|AAAA|CNAME] [--ttl N] [--dir PATH]")
	dir := addStoreFlag(fs)
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
	a, cfg, err := openConfig(*dir)
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
	fs := newFlagSet("remove", "usage: devdns remove <name> [--type A|AAAA|CNAME] [--dir PATH]")
	dir := addStoreFlag(fs)
	typ := fs.String("type", "", "only remove records of this type")
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		fs.Usage()
		return fmt.Errorf("expected a single <name>")
	}
	a, cfg, err := openConfig(*dir)
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
	fs := newFlagSet("validate", "usage: devdns validate [--dir PATH]")
	dir := addStoreFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	_, cfg, err := openConfig(*dir)
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
	fs := newFlagSet("generate", "usage: devdns generate [--dir PATH]")
	dir := addStoreFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	a, cfg, err := openConfig(*dir)
	if err != nil {
		return err
	}
	if err := a.regenerate(cfg); err != nil {
		return err
	}
	fmt.Printf("Generated %s and %s\n", rel(a.dir, a.corefile), a.zoneRelPath(cfg.Zone))
	a.reloadNotice()
	return nil
}

// ensurePrivileges fails fast when binding the given port needs elevated
// privileges the current process lacks, before any CoreDNS download or start.
// Ports below 1024 -- including the standard DNS port 53 -- require root on
// Unix; the check is skipped on Windows, which has no euid or sudo.
func ensurePrivileges(port int, recordsPath string) error {
	if port >= 1024 || runtime.GOOS == "windows" || os.Geteuid() == 0 {
		return nil
	}
	return fmt.Errorf("binding port %d requires root; re-run with sudo:\n\n"+
		"    sudo devdns start\n\n"+
		"or set a port >= 1024 (e.g. `port: 1053`) in %s to run without sudo",
		port, recordsPath)
}

func cmdStart(args []string) error {
	fs := newFlagSet("start", "usage: devdns start [--dir PATH] [--coredns PATH] [--no-download]")
	dir := addStoreFlag(fs)
	binary := fs.String("coredns", "", "path to the coredns binary")
	noDownload := fs.Bool("no-download", false, "do not auto-download CoreDNS if it is missing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	a, cfg, err := openConfig(*dir)
	if err != nil {
		return err
	}
	if err := ensurePrivileges(cfg.ResolvedPort(), a.recordsPath); err != nil {
		return err
	}
	if err := a.regenerate(cfg); err != nil {
		return err
	}
	bin, err := coredns.EnsureBinary(*binary, []string{a.globalHome, a.dir}, a.globalHome, downloadAllowed(*noDownload), logStderr)
	if err != nil {
		return err
	}
	msg, err := a.manager(bin).Start()
	if err != nil {
		return err
	}
	fmt.Println(msg)
	fmt.Printf("Serving zone %s on %s:%d; forwarding everything else to %s\n",
		cfg.Zone, listenAddr(cfg), cfg.ResolvedPort(), strings.Join(cfg.ResolvedUpstreams(), ", "))
	return nil
}

func cmdStop(args []string) error {
	fs := newFlagSet("stop", "usage: devdns stop [--dir PATH]")
	dir := addStoreFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	a, err := openStore(*dir)
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
	fs := newFlagSet("restart", "usage: devdns restart [--dir PATH] [--coredns PATH] [--no-download]")
	dir := addStoreFlag(fs)
	binary := fs.String("coredns", "", "path to the coredns binary")
	noDownload := fs.Bool("no-download", false, "do not auto-download CoreDNS if it is missing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	a, cfg, err := openConfig(*dir)
	if err != nil {
		return err
	}
	if err := ensurePrivileges(cfg.ResolvedPort(), a.recordsPath); err != nil {
		return err
	}
	if err := a.regenerate(cfg); err != nil {
		return err
	}
	bin, err := coredns.EnsureBinary(*binary, []string{a.globalHome, a.dir}, a.globalHome, downloadAllowed(*noDownload), logStderr)
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
	fs := newFlagSet("reload", "usage: devdns reload [--dir PATH]")
	dir := addStoreFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	a, cfg, err := openConfig(*dir)
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
	fs := newFlagSet("status", "usage: devdns status [--dir PATH]")
	dir := addStoreFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	a, err := openStore(*dir)
	if err != nil {
		return err
	}
	st := a.manager("").Status()
	if st.Running {
		fmt.Printf("CoreDNS:  running (pid %d)\n", st.PID)
	} else {
		fmt.Println("CoreDNS:  stopped")
	}
	fmt.Printf("Store:    %s (%s)\n", a.dir, a.kind)
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
	fs := newFlagSet("install-coredns", "usage: devdns install-coredns [--dir PATH] [--coredns-version V]")
	dir := addStoreFlag(fs)
	ver := fs.String("coredns-version", "", "CoreDNS version to download (default "+coredns.DefaultVersion+")")
	if err := fs.Parse(args); err != nil {
		return err
	}
	a, err := openStore(*dir)
	if err != nil {
		return err
	}
	binPath, err := coredns.Install(coredns.InstallOptions{
		Version: *ver,
		Dest:    coredns.DefaultDest(a.globalHome),
		Log:     logStderr,
	})
	if err != nil {
		return err
	}
	fmt.Println(binPath)
	return nil
}

// ---- shared helpers ----

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

devdns stores everything for a zone in a store directory: a project-local
./.devdns (found by walking up from the current directory) takes precedence
over the global ~/.devdns, which is created automatically on first use.

Store commands:
  init                    Create a store: ./.devdns here, or ~/.devdns with --global
  where                   Print the active store directory and how it was chosen

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
  install-coredns         Download CoreDNS into ~/.devdns/bin (auto-runs at start)

Other:
  version                 Print the devdns version
  help                    Show this help

Every command accepts --dir <path> to act on a specific store. The global home
is overridable with $DEVDNS_HOME.

Examples:
  devdns init --zone example.internal
  devdns add app 127.0.0.1
  devdns add api.example.internal 127.0.0.1
  devdns add www app --type CNAME
  devdns remove api
  devdns generate && sudo devdns start
  dig @127.0.0.1 app.example.internal
`)
}
