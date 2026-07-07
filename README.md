# devdns — local development DNS

A small, self-contained local DNS resolver for development. It serves an
authoritative zone for your own services (for example `app.example.internal`)
and forwards everything else (`google.com`, `github.com`, …) to upstream public
resolvers. DNS is served by [CoreDNS](https://coredns.io/); a Go CLI named
`devdns` manages your records and the CoreDNS lifecycle.

- **No `/etc/hosts` editing** — real DNS, so wildcards, `dig`, and every library resolve correctly.
- **No Kubernetes, no cloud account.**
- **One editable file** — `records.yaml` is the single source of truth.
- **Public DNS keeps working** — anything outside your zone is forwarded upstream.

---

## Contents

- [Requirements](#requirements)
- [Install](#install)
- [Quick start](#quick-start)
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
  `devdns start` downloads the right binary for your OS/architecture into `./bin`
  if it is missing. You can also pre-fetch it (`devdns install-coredns` or
  `make coredns`), `brew install coredns`, or point `DEVDNS_COREDNS` at an
  existing binary.
- `dig` / `nslookup` for verification (preinstalled on macOS and most Linux).

Generating and validating config needs only Go — CoreDNS is not required for that.
The downloader is pure Go (no `curl`/`tar` needed) and works on macOS, Linux, and
Windows.

## Install

```bash
# 1. Build the CLI (produces ./bin/devdns)
make build

# 2. CoreDNS is fetched automatically on the first `devdns start`.
#    To pre-fetch it (e.g. in CI or offline prep), choose any of:
make coredns                  # OS-agnostic Go downloader -> ./bin/coredns
./bin/devdns install-coredns  # same thing, directly
# or: brew install coredns
# or: export DEVDNS_COREDNS=/path/to/coredns

# Optional: install devdns onto your PATH
make install                  # go install ./cmd/devdns
```

`devdns` looks for the CoreDNS binary in this order: `--coredns` flag →
`$DEVDNS_COREDNS` → `./bin/coredns` → `$PATH`. If none is found, `start` and
`restart` download it into `./bin`. Disable that with `--no-download` or
`DEVDNS_NO_DOWNLOAD=1`, and pick a version with `DEVDNS_COREDNS_VERSION`.

## Quick start

The shipped `records.yaml` uses port **1053**, which needs no `sudo`.

```bash
make build

./bin/devdns generate      # write Corefile + zones/ from records.yaml
./bin/devdns start         # downloads CoreDNS if missing, then starts it
./bin/devdns status

# Local zone resolves:
dig @127.0.0.1 -p 1053 app.example.internal +short      # -> 127.0.0.1
# Public DNS is forwarded:
dig @127.0.0.1 -p 1053 example.com +short

./bin/devdns stop
```

To make your whole machine use it, see
[Point your OS at devdns](#point-your-os-at-devdns).

## How it works

`records.yaml` → `devdns generate` → a **Corefile** and a **zone file**, which
CoreDNS serves:

```
                         ┌─────────────────────────────┐
  app.example.internal ─▶│ CoreDNS                      │
                         │  example.internal:PORT  ─────┼─▶ zones/example.internal.db
  github.com ───────────▶│  .:PORT (forward)       ─────┼─▶ 1.1.1.1 8.8.8.8 …
                         └─────────────────────────────┘
```

The generated Corefile has two server blocks:

```
example.internal:1053 {      # authoritative for your zone
    file zones/example.internal.db { reload 5s }
    log
    errors
}

.:1053 {                     # everything else
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

`records.yaml` is edited by hand or via the CLI:

```yaml
zone: example.internal      # required: the authoritative local zone
ttl: 60                     # optional: default record TTL (default 60)
port: 1053                  # optional: listen port (default 53)
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

Every command accepts `--file <path>` (default `records.yaml`).

| Command | Description |
| --- | --- |
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
| `install-coredns [--coredns-version V]` | Download CoreDNS into `./bin` (auto-runs at start) |
| `version` | Print the version |

**Type inference:** `add`/`update` infer the type from the value — a valid IPv4
→ `A`, IPv6 → `AAAA`, otherwise `CNAME`. A value that looks like an IP but is
invalid is rejected rather than silently made a CNAME. Use `--type` to be
explicit.

Examples:

```bash
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

### macOS (recommended: per-domain, no sudo for CoreDNS)

macOS reads `/etc/resolver/<domain>` files to route a single domain to a specific
resolver — including a custom port. This keeps your normal DNS untouched and
only sends the local zone to devdns, so CoreDNS can stay on port 1053:

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

### macOS / any OS (whole-system resolver)

Point all DNS at CoreDNS and let it forward public queries. This needs CoreDNS on
**port 53** (set `port: 53` in `records.yaml`, `devdns generate`, then start with
`sudo`, or use Docker). Then set your DNS server to `127.0.0.1`
(System Settings → Network → … → DNS on macOS, or your resolver config on Linux).

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

- **Port 1053 (default here):** no `sudo`. Reach it with `dig @127.0.0.1 -p 1053 …`
  or route a domain to it via `/etc/resolver` (macOS) / systemd-resolved (Linux).
- **Port 53:** set `port: 53` in `records.yaml`, `devdns generate`, then
  `sudo ./bin/devdns start` (or run it under Docker, which maps the port for
  you). `devdns start` detects a permission failure and reminds you of these
  options.

Set `address: 127.0.0.1` if you want to avoid exposing the resolver on your LAN
(leave it unset for Docker, which needs to listen on all interfaces).

## Docker

Run CoreDNS in a container instead of natively (no local CoreDNS install needed).
Docker publishes on host port 53, so use port 53 in the config:

```bash
# set port: 53 in records.yaml (leave `address` unset), then:
devdns generate
docker compose up -d

dig @127.0.0.1 app.example.internal +short
dig @127.0.0.1 github.com +short

docker compose down
```

The container mounts the generated `Corefile` and `zones/` read-only. Keep
editing records with `devdns` on the host and re-run `devdns generate`; CoreDNS
reloads automatically.

## Verify

```bash
# Local zone (authoritative answer from devdns)
dig @127.0.0.1 -p 1053 app.example.internal
nslookup -port=1053 api.example.internal 127.0.0.1

# Public name (forwarded upstream)
dig @127.0.0.1 -p 1053 google.com

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

**`permission denied` / port 53** — you tried to bind port 53 without privileges.
Use port 1053 (the default) or run with `sudo` / Docker. See
[Ports and permissions](#ports-and-permissions-53-vs-1053).

**`address already in use`** — something already listens on the port. Find it
with `sudo lsof -nP -iUDP:1053` (or `:53`), then stop it or change `port:`.

**`dig` works but `ping`/the browser doesn't** — `dig @127.0.0.1` talks to
CoreDNS directly; the OS only uses it after you
[point your OS at devdns](#point-your-os-at-devdns). Note `dig` (without `@`)
ignores macOS `/etc/resolver` files — use `ping`/`scutil --dns` to test those.

**Changes not taking effect** — CoreDNS reloads within ~5s; check
`./bin/devdns status` and the log at `.devdns/coredns.log`. `devdns restart`
forces an immediate reload. macOS caches DNS: `sudo dscacheutil -flushcache;
sudo killall -HUP mDNSResponder`.

**Inspect CoreDNS logs** — `.devdns/coredns.log` (queries, errors, reloads).

## Project layout

```
.
├── cmd/devdns/          # CLI entrypoint
├── internal/
│   ├── records/         # load/validate/mutate/persist records.yaml
│   ├── validation/      # hostname / IP / record-type checks
│   ├── generator/       # render Corefile + zone files (pure funcs)
│   └── coredns/         # start/stop/status + OS-agnostic downloader
├── zones/               # generated zone files
├── scripts/             # install-coredns.sh (bash fallback; devdns fetches it natively)
├── records.yaml         # your records (source of truth)
├── Corefile             # generated CoreDNS config
├── docker-compose.yml   # optional containerized CoreDNS
└── Makefile
```

`Corefile` and `zones/*.db` are generated from `records.yaml`; they are checked
in as a ready-to-run sample and regenerated by `devdns generate`.

## Development

```bash
make test      # go test ./...
make vet       # go vet ./...
make fmt       # go fmt ./...
make build     # ./bin/devdns
```

Tests cover hostname/IP/type validation, record parsing and mutation
(duplicates, CNAME rules, add/update/remove), and zone/Corefile generation.
