You are a senior systems engineer. Implement a standalone local DNS development project using CoreDNS.

## Goal

Build a local development DNS resolver that lets me seed and manage locally resolvable DNS records for my API/services, while forwarding all normal public DNS requests upstream.

Example:

* `app.example.internal` resolves locally
* `api.example.internal` resolves locally
* `auth.example.internal` resolves locally
* `google.com`, `github.com`, etc. continue resolving normally

Avoid using `.local`; prefer `example.internal` or another configurable local dev zone.

## Required Features

1. Use CoreDNS as the DNS server.
2. Provide a standalone project with clear setup instructions.
3. Support easy add/remove/update of local DNS records.
4. Public DNS requests must continue working through upstream forwarding.
5. Prefer a robust scripted/programmatic solution if it improves usability.
6. Language preference for helper tooling:

    * Go first
    * Python second
    * JavaScript third
7. The project should be usable on a local developer machine.
8. Records should be stored in a simple editable format, preferably YAML or JSON.
9. Generate or update CoreDNS-compatible configuration/zone files from the records file.
10. Include commands for:

    * adding a record
    * removing a record
    * listing records
    * regenerating CoreDNS config
    * starting CoreDNS
    * stopping CoreDNS
    * reloading CoreDNS
11. Include a safe default upstream DNS configuration, such as Cloudflare and Google DNS.
12. Include validation for:

    * duplicate hostnames
    * invalid hostnames
    * invalid IP addresses
    * unsupported record types
13. Support at least:

    * A records
    * AAAA records if easy
    * CNAME records if safe and practical
14. Include a README with installation, usage, troubleshooting, and examples.
15. Include a sample configuration with at least:

    * `app.example.internal -> 127.0.0.1`
    * `api.example.internal -> 127.0.0.1`
    * `auth.example.internal -> 127.0.0.1`
16. Include guidance for configuring the local OS to use this DNS server.
17. Include a Docker Compose option if practical, but the project should also explain native local usage.
18. Include a verification section using commands like:

    * `dig app.example.internal`
    * `dig google.com`
    * `nslookup api.example.internal`

## Preferred Architecture

Use this structure unless there is a better reason not to:

```text
local-dev-dns/
  README.md
  Corefile
  records.yaml
  zones/
    example.internal.db
  cmd/
    devdns/
      main.go
  internal/
    records/
    generator/
    validation/
    coredns/
  scripts/
  docker-compose.yml
  Makefile
```

## Desired CLI

Implement a CLI named `devdns`.

Example commands:

```bash
devdns list
devdns add app.example.internal 127.0.0.1
devdns add api.example.internal 127.0.0.1
devdns remove api.example.internal
devdns generate
devdns start
devdns stop
devdns reload
devdns status
```

The CLI should update `records.yaml`, regenerate zone files, and reload/restart CoreDNS where appropriate.

## CoreDNS Behavior

CoreDNS should be authoritative for the local development zone only.

Example behavior:

```text
*.example.internal -> local zone records
everything else -> forwarded to upstream DNS
```

A sample Corefile should look conceptually like:

```text
example.internal:53 {
    file zones/example.internal.db
    log
    errors
}

.:53 {
    forward . 1.1.1.1 8.8.8.8
    cache 30
    log
    errors
}
```

Adjust as needed for correctness and production-quality local behavior.

## Records File Example

Use a simple format like:

```yaml
zone: example.internal
records:
  - name: app
    type: A
    value: 127.0.0.1
  - name: api
    type: A
    value: 127.0.0.1
  - name: auth
    type: A
    value: 127.0.0.1
```

The CLI should allow both short names and FQDNs where possible:

```bash
devdns add app 127.0.0.1
devdns add app.example.internal 127.0.0.1
```

## Quality Requirements

* Keep the implementation simple but production-minded.
* Use clear error messages.
* Avoid hidden global state.
* Make commands idempotent where reasonable.
* Include tests for parsing, validation, and zone-file generation.
* Do not require Kubernetes.
* Do not require a cloud account.
* Do not require editing `/etc/hosts`.
* Prefer minimal dependencies.
* Explain any permissions needed to bind to port 53.
* Include alternatives if port 53 cannot be used directly, such as running on `127.0.0.1:1053`.

## Deliverables

Produce the full project files.

Include:

1. Source code
2. CoreDNS config
3. Zone generation logic
4. Sample `records.yaml`
5. README
6. Makefile
7. Docker Compose file if practical
8. Tests
9. Example usage flow
10. Troubleshooting section

## Final Output

After implementation, summarize:

* what was created
* how to run it
* how to add/remove records
* how to verify local DNS resolution
* how public DNS forwarding is preserved
