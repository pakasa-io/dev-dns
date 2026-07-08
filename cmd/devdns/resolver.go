package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"dev-dns/internal/resolver"
)

// cmdResolver manages the OS-level per-domain route that makes the active
// store's zone resolve system-wide (getaddrinfo/ping/browser), pointing it at
// the local CoreDNS. Only writing that route needs root; the store and CoreDNS
// stay unprivileged, so devdns elevates just the apply step.
func cmdResolver(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: devdns resolver <install|uninstall|status> [--dir PATH]")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "install":
		return cmdResolverRun("install", rest)
	case "uninstall", "remove", "rm":
		return cmdResolverRun("uninstall", rest)
	case "status":
		return cmdResolverStatus(rest)
	default:
		return fmt.Errorf("unknown resolver subcommand %q (want install|uninstall|status)", sub)
	}
}

func cmdResolverRun(verb string, args []string) error {
	fs := newFlagSet("resolver "+verb, "usage: devdns resolver "+verb+" [--dir PATH] [--print]")
	dir := addStoreFlag(fs)
	printOnly := fs.Bool("print", false, "print the exact sudo commands instead of running them")
	apply := fs.Bool("apply", false, "internal: perform the privileged change (used by the elevated step)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	route, a, err := routeFromStore(*dir)
	if err != nil {
		return err
	}
	plan := resolver.InstallPlan(route)
	if verb == "uninstall" {
		plan = resolver.UninstallPlan(route)
	}

	// Elevated child: do only the privileged change, nothing else.
	if *apply {
		if os.Geteuid() != 0 {
			return fmt.Errorf("--apply must run as root")
		}
		return resolver.Apply(plan)
	}

	if !plan.Supported {
		fmt.Printf("No automated resolver backend on %s. Configure it manually:\n\n", runtime.GOOS)
		printManual(plan)
		return nil
	}

	previewResolver(verb, route, plan)

	if *printOnly {
		fmt.Println("\nRun:")
		printManual(plan)
		return nil
	}

	if os.Geteuid() == 0 {
		if err := resolver.Apply(plan); err != nil {
			return err
		}
	} else if err := elevateResolver(verb, a.dir); err != nil {
		fmt.Fprintf(os.Stderr, "\ncould not run sudo (%v); do it yourself:\n", err)
		printManual(plan)
		return err
	}

	ok, path := resolver.Installed(route)
	switch {
	case verb == "install" && ok:
		fmt.Printf("\nRouted %s -> %s:%d (%s).\nNames under %s now resolve system-wide (e.g. `ping app.%s`).\n",
			route.Zone, route.Addr, portOr53(route.Port), path, route.Zone, route.Zone)
	case verb == "uninstall" && !ok:
		fmt.Printf("\nRemoved the OS route for %s.\n", route.Zone)
	}
	return nil
}

func cmdResolverStatus(args []string) error {
	fs := newFlagSet("resolver status", "usage: devdns resolver status [--dir PATH]")
	dir := addStoreFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	route, _, err := routeFromStore(*dir)
	if err != nil {
		return err
	}
	plan := resolver.InstallPlan(route)
	if !plan.Supported {
		fmt.Printf("Zone %s: no automated resolver backend on %s (route it manually).\n", route.Zone, runtime.GOOS)
		return nil
	}
	if ok, path := resolver.Installed(route); ok {
		fmt.Printf("Zone %s: routed to %s:%d via %s (%s).\n", route.Zone, route.Addr, portOr53(route.Port), plan.Backend, path)
	} else {
		fmt.Printf("Zone %s: not routed -- run `devdns resolver install`. Backend: %s.\n", route.Zone, plan.Backend)
	}
	return nil
}

// routeFromStore resolves the active store and builds the Route for its zone.
func routeFromStore(dir string) (resolver.Route, *app, error) {
	a, cfg, err := openConfig(dir)
	if err != nil {
		return resolver.Route{}, nil, err
	}
	return resolver.Route{Zone: cfg.Zone, Addr: "127.0.0.1", Port: cfg.ResolvedPort()}, a, nil
}

// elevateResolver re-runs devdns under sudo to perform only the privileged
// resolver change for the given store, passing the resolved store dir so the
// root child reads exactly the same store (and never auto-inits anything).
func elevateResolver(verb, storeDir string) error {
	if _, err := exec.LookPath("sudo"); err != nil {
		return fmt.Errorf("sudo not found")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	c := exec.Command("sudo", exe, "resolver", verb, "--apply", "--dir", storeDir)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	return c.Run()
}

func previewResolver(verb string, route resolver.Route, plan resolver.Plan) {
	if verb == "install" {
		fmt.Printf("Routing %s -> %s:%d via %s (sudo needed for this one step):\n",
			route.Zone, route.Addr, portOr53(route.Port), plan.Backend)
		fmt.Printf("  write %s\n", plan.Path)
		for _, line := range strings.Split(strings.TrimRight(plan.Content, "\n"), "\n") {
			fmt.Printf("      %s\n", line)
		}
	} else {
		fmt.Printf("Removing route for %s (%s) via %s (sudo needed):\n", route.Zone, plan.Path, plan.Backend)
	}
}

func printManual(p resolver.Plan) {
	for _, s := range p.Manual {
		fmt.Println("  " + s)
	}
}

func portOr53(p int) int {
	if p == 0 {
		return 53
	}
	return p
}
