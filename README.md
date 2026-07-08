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
`example.internal`, port **1053** — no `sudo` to run), so from the repo root:

```bash
make build

./bin/devdns start         # runs as you (port 1053); downloads CoreDNS if missing
./bin/devdns status

# Local zone resolves (high port, so pass -p to dig):
dig @127.0.0.1 -p 1053 app.example.internal +short   # -> 127.0.0.1
# Public DNS is forwarded:
dig @127.0.0.1 -p 1053 example.com +short

./bin/devdns stop
```

For a **new** project of your own, one command scaffolds the store **and** routes
the zone into your OS so apps resolve it — the store is created as you, and only
the route step uses `sudo`:

```bash
cd ~/my-project
devdns init --system --zone my-project.internal   # store as you; routes the zone (sudo only for that)
devdns add app                                     # app.my-project.internal -> 127.0.0.1
devdns start                                       # no sudo (port 1053)
ping app.my-project.internal                       # resolves system-wide
```

Drop `--system` to create the store only; run `devdns resolver install` later to
route it.

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
port: 1053                  # optional: listen port; 1053 (default) needs no sudo, 53 does
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
| `init [--system] [--global] [--zone Z] [--port N]` | Create a store (`./.devdns`, or `~/.devdns` with `--global`); `--system` also routes the zone into your OS |
| `where` | Print the active store directory and how it was chosen |
| `list [--json]` | List records |
| `add <name> [value] [--type T] [--ttl N]` | Add a record; `value` defaults to `127.0.0.1` (type inferred) |
| `update <name> <value> [--type T] [--ttl N]` | Add or replace a record |
| `remove <name> [--type T]` | Remove records for a name |
| `validate` | Validate `records.yaml` |
| `generate` | Regenerate the Corefile + zone file |
| `start [--coredns PATH] [--no-download]` | Start CoreDNS (downloads it if missing) |
| `stop` | Stop CoreDNS |
| `restart [--coredns PATH] [--no-download]` | Restart CoreDNS (downloads it if missing) |
| `reload` | Regenerate config; a running CoreDNS reloads itself |
| `status` | Show CoreDNS status and configuration |
| `resolver install` | Route the zone into your OS so it resolves system-wide (also `uninstall`, `status`); `sudo` only for the write |
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
devdns add app                           # A record -> 127.0.0.1 (default value)
devdns add api 10.0.0.5                   # explicit value; type A inferred
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

Serving the zone in CoreDNS isn't enough — your OS only sends a domain to devdns
once you **route** it there (otherwise `getaddrinfo`/Node/`ping` get NXDOMAIN).
`devdns resolver install` does this for you, OS-aware, elevating only the one
step that needs root:

```bash
devdns resolver install       # write the per-domain route (sudo just for this step)
devdns resolver status        # is the zone routed?
devdns resolver uninstall     # remove it
```

- **macOS** → `/etc/resolver/<zone>`
- **Linux (systemd-resolved)** → `/etc/systemd/resolved.conf.d/devdns-<zone>.conf`
- **other** (Windows, non-systemd Linux) → it prints the exact manual steps.

Add `--print` to see the commands without running `sudo`. Once installed,
`ping`/`curl`/your app resolve the zone system-wide. Note `dig @127.0.0.1` still
needs `-p <port>` — `dig` talks to CoreDNS directly and ignores the OS route.

### Whole-system resolver (port 53)

To make devdns your machine's *only* resolver instead of routing one zone, set
`port: 53` (needs `sudo` to run, or Docker) and point all DNS at `127.0.0.1`
(System Settings → Network → … → DNS on macOS; your stub-resolver config on Linux).

## Ports and permissions (53 vs 1053)

Ports below 1024 (including 53) require root to bind:

- **Port 1053 (default):** no `sudo` — devdns runs as you and leaves no root-owned
  files. Route the zone with `devdns resolver install`; reach it directly with
  `dig @127.0.0.1 -p 1053 …`. The recommended dev setup.
- **Port 53:** the standard DNS port, so `devdns start` needs `sudo` (or Docker).
  Use it for a whole-system resolver. `devdns start` refuses up front when you are
  not root, and hands the store back to your user afterward (no root-owned files).

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
CoreDNS directly; the OS only uses it once the zone is routed. Run
`devdns resolver install` (see [Point your OS at devdns](#point-your-os-at-devdns)),
then `devdns resolver status` to confirm. Note plain `dig` ignores the OS route —
use `ping`/`scutil --dns` to test that path.

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
