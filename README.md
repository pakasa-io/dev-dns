# devdns — local development DNS

A small, self-contained local DNS resolver for development. It serves an
authoritative zone for your own services (for example `app.example.internal`)
and forwards everything else (`google.com`, `github.com`, …) to upstream public
resolvers. DNS is served by [CoreDNS](https://coredns.io/); a Go CLI named
`devdns` manages your records and the CoreDNS lifecycle.

- **No `/etc/hosts` editing** — real DNS, so wildcards, `dig`, and every library resolve correctly.
- **No Kubernetes, no cloud account.**
- **Git-style stores** — a project-local `./.devdns/` takes precedence over a global `~/.devdns/`.
- **One editable file** — `records.yaml` inside the store is the single source of truth.
- **Public DNS keeps working** — anything outside your zone is forwarded upstream.

---

## Contents

- [Requirements](#requirements)
- [Install](#install)
- [Quick start](#quick-start)
- [Stores (global vs project)](#stores-global-vs-project)
- [How it works](#how-it-works)
- [The records file](#the-records-file)
- [CLI reference](#cli-reference)
- [Point your OS at devdns](#point-your-os-at-devdns)
- [Ports and permissions (53 vs 1053)](#ports-and-permissions-53-vs-1053)
- [Docker](#docker)
- [Verify](#verify)
- [Troubleshooting](#troubleshooting)
- [Project layout](#project-layout)
- [Development](#development)

---

## Requirements

- **Go** ≥ 1.23 (to build the `devdns` CLI).
- **CoreDNS** — only needed to actually serve DNS, and **fetched automatically**:
  `devdns start` downloads the right binary for your OS/architecture into
  `~/.devdns/bin` if it is missing. You can also pre-fetch it
  (`devdns install-coredns` or `make coredns`), `brew install coredns`, or point
  `DEVDNS_COREDNS` at an existing binary.
- `dig` / `nslookup` for verification (preinstalled on macOS and most Linux).

Generating and validating config needs only Go — CoreDNS is not required for that.
The downloader is pure Go (no `curl`/`tar` needed) and works on macOS, Linux, and
Windows.

## Install

### Homebrew (macOS/Linux)

```bash
brew install pakasa-io/tap/devdns
```

This also installs `coredns` as a dependency. To upgrade, refresh the tap first:

```bash
brew update && brew upgrade devdns
```

(`brew upgrade devdns` alone can report "already installed" if you have
`HOMEBREW_NO_AUTO_UPDATE=1` set — that skips the tap refresh, so Homebrew never
sees the new release. `brew update` pulls the latest formula.)

### From source

```bash
# 1. Build the CLI (produces ./bin/devdns)
make build

# 2. CoreDNS is fetched automatically on the first `devdns start`.
#    To pre-fetch it (e.g. in CI or offline prep), choose any of:
make coredns                  # OS-agnostic Go downloader -> ~/.devdns/bin/coredns
./bin/devdns install-coredns  # same thing, directly
# or: brew install coredns
# or: export DEVDNS_COREDNS=/path/to/coredns

# Optional: install devdns onto your PATH
make install                  # go install ./cmd/devdns
```

The **global store** `~/.devdns/` is created automatically the first time you run
a command outside any project — so devdns works out of the box after install.
`devdns` looks for the CoreDNS binary in this order: `--coredns` flag →
`$DEVDNS_COREDNS` → `~/.devdns/bin/coredns` → the active store's `bin/coredns` →
`$PATH`. If none is found, `start`/`restart` download it into `~/.devdns/bin`.
Disable that with `--no-download` or `DEVDNS_NO_DOWNLOAD=1`, and pick a version
with `DEVDNS_COREDNS_VERSION`.

## Quick start

This repository ships a ready-to-run project store in `./.devdns/` (zone
`example.internal`, port **53**), so from the repo root:

```bash
make build

sudo ./bin/devdns start    # port 53 needs root; walks up to ./.devdns, downloads CoreDNS if missing
sudo ./bin/devdns status

# Local zone resolves:
dig @127.0.0.1 app.example.internal +short      # -> 127.0.0.1
# Public DNS is forwarded:
dig @127.0.0.1 example.com +short

sudo ./bin/devdns stop
```

For a **new** project of your own, scaffold a store first:

```bash
cd ~/my-project
devdns init --zone my-project.internal   # creates ./.devdns with a starter records.yaml (port 53)
devdns add app 127.0.0.1
sudo devdns start                        # port 53 needs root; or `devdns init --port 1053` for no sudo
dig @127.0.0.1 app.my-project.internal +short
```

To make your whole machine use it, see
[Point your OS at devdns](#point-your-os-at-devdns).

## Stores (global vs project)

devdns keeps everything for a zone in a **store** directory: `records.yaml`, the
generated `Corefile` and `zones/`, and the CoreDNS pidfile/log all live inside it.

- **`~/.devdns/`** — the **global** store, created automatically on first use. Used
  when you are not inside a project that has its own store. It also holds the
  shared CoreDNS binary at `~/.devdns/bin/coredns`, reused by every project.
- **`./.devdns/`** — a **project** store, created by `devdns init`. It **takes
  precedence** and is discovered by walking up from the current directory
  (git-style), so you can run `devdns` from any subfolder of your project.

Resolution order for every command: `--dir <path>` if given → the nearest
`./.devdns/` walking up from the current directory → the global `~/.devdns/`.

```bash
devdns where            # print the active store and how it was chosen
devdns init             # create ./.devdns here
devdns init --global    # (re)initialize ~/.devdns
devdns --dir /path/to/store status
```

Point the global home somewhere else with `DEVDNS_HOME` (handy for tests or
keeping multiple isolated homes).

## How it works

`records.yaml` → `devdns generate` → a **Corefile** and a **zone file** inside the
store, which CoreDNS serves:

```
                         ┌─────────────────────────────┐
  app.example.internal ─▶│ CoreDNS                      │
                         │  example.internal:PORT  ─────┼─▶ .devdns/zones/example.internal.db
  github.com ───────────▶│  .:PORT (forward)       ─────┼─▶ 1.1.1.1 8.8.8.8 …
                         └─────────────────────────────┘
```

The generated Corefile has two server blocks:

```
example.internal:53 {        # authoritative for your zone
    file zones/example.internal.db { reload 5s }
    log
    errors
}

.:53 {                       # everything else
    forward . 1.1.1.1 1.0.0.1 8.8.8.8 8.8.4.4
    cache 30
    reload 5s
    log
    errors
}
```

Because both blocks enable `reload`, a running CoreDNS picks up regenerated files
within ~5 seconds — so `devdns add/remove/update/reload` take effect without a
restart. Each generate bumps the zone's SOA serial so the change is detected.

## The records file

`.devdns/records.yaml` is edited by hand or via the CLI:

```yaml
zone: example.internal      # required: the authoritative local zone
ttl: 60                     # optional: default record TTL (default 60)
port: 53                    # optional: listen port; 53 needs sudo, >=1024 (e.g. 1053) doesn't
# address: 127.0.0.1        # optional: bind one address; omit = all interfaces
upstreams:                  # optional: forwarders (default: Cloudflare + Google)
  - 1.1.1.1
  - 1.0.0.1
  - 8.8.8.8
  - 8.8.4.4
records:
  - name: app               # relative to the zone; "@" is the apex
    type: A                 # A, AAAA, or CNAME
    value: 127.0.0.1
    # ttl: 300              # optional per-record TTL override
  - name: api
    type: A
    value: 127.0.0.1
  - name: auth
    type: A
    value: 127.0.0.1
```

Rules enforced by validation:

- **Names** must be valid hostnames; a leading `*` (wildcard) label is allowed.
  You may write short names (`app`) or fully qualified (`app.example.internal`);
  they are normalized to short form.
- **Values** must match the type: A → IPv4, AAAA → IPv6, CNAME → hostname.
- **No duplicates**: at most one record per `(name, type)`.
- **CNAME exclusivity**: a name with a CNAME may not also have other records.

> Note: mutating the file with the CLI rewrites it as canonical YAML and **does
> not preserve comments**. Keep comments in a copy if you need them, or manage
> records purely by hand.

## CLI reference

```
devdns <command> [flags]
```

Every command accepts `--dir <path>` to act on a specific store (default: the
nearest `./.devdns`, else `~/.devdns`).

| Command | Description |
| --- | --- |
| `init [--global] [--zone Z] [--port N] [--force]` | Create a store (`./.devdns` here, or `~/.devdns` with `--global`) |
| `where` | Print the active store directory and how it was chosen |
| `list [--json]` | List records |
| `add <name> <value> [--type T] [--ttl N]` | Add a record (type inferred from value) |
| `update <name> <value> [--type T] [--ttl N]` | Add or replace a record |
| `remove <name> [--type T]` | Remove records for a name |
| `validate` | Validate `records.yaml` |
| `generate` | Regenerate the Corefile + zone file |
| `start [--coredns PATH] [--no-download]` | Start CoreDNS (downloads it if missing) |
| `stop` | Stop CoreDNS |
| `restart [--coredns PATH] [--no-download]` | Restart CoreDNS (downloads it if missing) |
| `reload` | Regenerate config; a running CoreDNS reloads itself |
| `status` | Show CoreDNS status and configuration |
| `install-coredns [--coredns-version V]` | Download CoreDNS into `~/.devdns/bin` (auto-runs at start) |
| `version` | Print the version |

**Type inference:** `add`/`update` infer the type from the value — a valid IPv4
→ `A`, IPv6 → `AAAA`, otherwise `CNAME`. A value that looks like an IP but is
invalid is rejected rather than silently made a CNAME. Use `--type` to be
explicit.

**Environment:** `DEVDNS_HOME` (override `~/.devdns`), `DEVDNS_COREDNS` (path to
an existing coredns binary), `DEVDNS_COREDNS_VERSION` (version to download), and
`DEVDNS_NO_DOWNLOAD` (disable auto-download).

Examples:

```bash
devdns init                              # scaffold ./.devdns for this project
devdns add app 127.0.0.1                 # A record (inferred)
devdns add api.example.internal 127.0.0.1
devdns add v6 ::1                         # AAAA (inferred)
devdns add www app --type CNAME          # www.example.internal -> app.example.internal
devdns add app 127.0.0.1 --ttl 300       # per-record TTL
devdns update auth 10.0.0.9              # replace auth's value
devdns remove api                        # remove all records named api
devdns remove app --type AAAA            # remove only the AAAA
devdns list --json
```

Adding, updating, removing, generating, and reloading all rewrite the generated
files; if CoreDNS is running it applies the change within a few seconds.

## Point your OS at devdns

Two ways, depending on your port. **Whole-system** works out of the box with the
default port 53 (devdns runs under `sudo`). **Per-domain** routes only your zone
and needs no `sudo`, but requires a high port (e.g. 1053).

### Per-domain, no sudo (port 1053)

Route only your local zone to devdns on a high port, leaving system DNS
untouched. First set the store to port 1053 (`devdns init --port 1053`, or
`port: 1053` then `devdns generate`). macOS reads `/etc/resolver/<domain>` files,
which support a custom port:

```bash
sudo mkdir -p /etc/resolver
sudo tee /etc/resolver/example.internal >/dev/null <<'EOF'
nameserver 127.0.0.1
port 1053
EOF

# verify (macOS resolver, not dig — dig ignores /etc/resolver)
scutil --dns | grep -A3 example.internal
ping app.example.internal
```

Remove it with `sudo rm /etc/resolver/example.internal`.

### Whole-system resolver (default, port 53)

With the default port 53, point all DNS at devdns and let it forward public
queries. Start devdns with `sudo` (or Docker), then set your DNS server to
`127.0.0.1` (System Settings → Network → … → DNS on macOS, or your resolver
config on Linux).

### Linux (systemd-resolved, per-domain)

```ini
# /etc/systemd/resolved.conf.d/example.internal.conf
[Resolve]
DNS=127.0.0.1:1053
Domains=~example.internal
```

```bash
sudo systemctl restart systemd-resolved
resolvectl status
```

(For a whole-system resolver, set `DNS=127.0.0.1` on port 53 and drop the
`Domains=` line.)

## Ports and permissions (53 vs 1053)

Binding to port **53** requires elevated privileges (it is below 1024):

- **Port 53 (default):** the standard DNS port, so `devdns start` needs `sudo`
  (or Docker, which maps it for you). Reach it with `dig @127.0.0.1 …` and use it
  as a whole-system resolver. `devdns start` refuses up front when you are not
  root and tells you how to proceed.
- **Port ≥ 1024 (e.g. 1053):** no `sudo`. Set it with `devdns init --port 1053`
  (or `port: 1053` then `devdns generate`). Reach it with `dig @127.0.0.1 -p 1053 …`
  or route a domain to it per-domain (see above).

Set `address: 127.0.0.1` if you want to avoid exposing the resolver on your LAN
(leave it unset for Docker, which needs to listen on all interfaces).

## Docker

Run CoreDNS in a container instead of natively (no local CoreDNS install needed).
Docker publishes on host port 53, so use port 53 in the config:

```bash
# set port: 53 in .devdns/records.yaml (leave `address` unset), then:
devdns generate
docker compose up -d

dig @127.0.0.1 app.example.internal +short
dig @127.0.0.1 github.com +short

docker compose down
```

The container mounts the generated `.devdns/Corefile` and `.devdns/zones/`
read-only. Keep editing records with `devdns` on the host and re-run
`devdns generate`; CoreDNS reloads automatically.

## Verify

```bash
# Local zone (authoritative answer from devdns) — default port 53:
dig @127.0.0.1 app.example.internal
nslookup api.example.internal 127.0.0.1

# Public name (forwarded upstream)
dig @127.0.0.1 google.com

# On a high port instead (port 1053), add -p:
dig @127.0.0.1 -p 1053 app.example.internal

# After OS integration (macOS /etc/resolver or system DNS), no @/port needed:
ping app.example.internal
```

A local answer shows your configured IP with `AUTHORITY`/`flags: aa`
(authoritative). A forwarded answer returns the real public record.

## Troubleshooting

**`coredns binary not found`** — normally `start` downloads CoreDNS
automatically; you only see this with `--no-download` / `DEVDNS_NO_DOWNLOAD`, or a
broken `--coredns` / `DEVDNS_COREDNS` path. Fix the path, run
`devdns install-coredns` (or `make coredns` / `brew install coredns`), or allow
the download. **Air-gapped:** install CoreDNS manually and set `DEVDNS_COREDNS`.

**`permission denied` / port 53** — port 53 needs root. `devdns start` checks up
front and tells you to re-run with `sudo` (or use Docker). To avoid sudo
entirely, switch to a high port: `devdns init --port 1053`. See
[Ports and permissions](#ports-and-permissions-53-vs-1053).

**`address already in use`** — something already listens on the port. Find it
with `sudo lsof -nP -iUDP:53` (or `:1053`), then stop it or change `port:`.

**Which store am I editing?** — run `devdns where`. A stray `~/.devdns` change
usually means you were outside any project; `cd` into the project (it needs a
`./.devdns/`, created by `devdns init`) or pass `--dir`.

**`dig` works but `ping`/the browser doesn't** — `dig @127.0.0.1` talks to
CoreDNS directly; the OS only uses it after you
[point your OS at devdns](#point-your-os-at-devdns). Note `dig` (without `@`)
ignores macOS `/etc/resolver` files — use `ping`/`scutil --dns` to test those.

**Changes not taking effect** — CoreDNS reloads within ~5s; check
`devdns status` and the log in the store (`.devdns/coredns.log`, or
`~/.devdns/coredns.log` for the global store). `devdns restart` forces an
immediate reload. macOS caches DNS: `sudo dscacheutil -flushcache;
sudo killall -HUP mDNSResponder`.

**Inspect CoreDNS logs** — `<store>/coredns.log` (queries, errors, reloads).

## Project layout

```
.
├── cmd/devdns/          # CLI entrypoint (built to ./bin/devdns)
├── internal/
│   ├── records/         # load/validate/mutate/persist records.yaml
│   ├── validation/      # hostname / IP / record-type checks
│   ├── generator/       # render Corefile + zone files (pure funcs)
│   └── coredns/         # start/stop/status + OS-agnostic downloader
├── .devdns/             # this project's store (only records.yaml is committed)
│   ├── records.yaml     #   your records (source of truth)
│   ├── Corefile         #   generated CoreDNS config (git-ignored)
│   └── zones/           #   generated zone files (git-ignored)
│       └── example.internal.db
├── scripts/             # install-coredns.sh (bash fallback; devdns fetches it natively)
├── docker-compose.yml   # optional containerized CoreDNS
└── Makefile
```

Inside a store, only `records.yaml` is committed. The generated `Corefile` and
`zones/*.db`, the runtime state (`coredns.pid`, `coredns.log`), and the shared
`bin/coredns` are all git-ignored — `devdns generate` (and `start`) recreate the
generated files from `records.yaml`. The **global** store lives at `~/.devdns/`
with the same layout.

## Development

```bash
make test      # go test ./...
make vet       # go vet ./...
make fmt       # go fmt ./...
make build     # ./bin/devdns
```

Tests cover store resolution (project walk-up vs global fallback), hostname/IP/
type validation, record parsing and mutation (duplicates, CNAME rules,
add/update/remove), and zone/Corefile generation.

### Releasing

Releases are cut by [GoReleaser](https://goreleaser.com) from a pushed tag
(`.goreleaser.yaml` + `.github/workflows/release.yml`): it cross-compiles
darwin/linux (amd64/arm64), publishes a GitHub release, and updates the Homebrew
formula in the tap.

One-time setup:

1. Create a public tap repo `<owner>/homebrew-tap`.
2. Add a repository secret `HOMEBREW_TAP_TOKEN` — a PAT with write access to it.
3. Set `owner` in `.goreleaser.yaml` (`brews.repository`) to your GitHub owner.

Each release:

```bash
make snapshot                 # optional: dry-run the build into ./dist (no publish)
git tag v0.1.0 && git push origin v0.1.0
```

The workflow does the rest; users then get it via `brew install <owner>/tap/devdns`.
