# Guide for AI agents

## Project purpose

goProxy is a cross-platform HTTP/HTTPS and SOCKS5 proxy written in Go. It
selects a direct, blocked, or upstream-proxy route using ordered hostname and IP
rules, loads external rule lists, binds outgoing connections to network
interfaces, and can run from the system tray.

Before making a non-trivial change, read:

- [the architecture map](.agents/architecture.md) to identify the owner of the
  relevant logic and its related contracts;
- [the verification guide](.agents/verification.md) to select appropriate
  checks and avoid running the application against the user's profile.

`README.md` documents user-facing configuration, builds, and installation.

## Repository map

- `main.go`: CLI, application lifecycle, reload coordination, servers, ticker,
  and tray integration.
- `proxy_servers.go`: ownership and clean shutdown of HTTP and SOCKS5
  listeners.
- `internal/handler/`: HTTP/CONNECT and SOCKS5 routing, direct/upstream dialing,
  self-connection protection, and UDP sessions.
- `internal/config/`: profiles, YAML configuration, external rules, rule-list
  caching, host indexes, and documented YAML/JSON Schema generation.
- `internal/cache/`: DNS, CIDR, and glob caches, including custom DNS and
  singleflight lookup coalescing.
- `internal/logging/`: asynchronous logging and file rotation.
- `internal/tray/`: full tray support for Windows/macOS and a headless Linux
  implementation.
- `internal/ticker/`: periodic external-rule reload signals.
- `assets/`: embedded icons and packaging resources.
- `scripts/`: binary builds, macOS bundling, Windows builds, and
  release/version automation.

## Core commands

Run these commands from the repository root:

```sh
gofmt -w <changed-go-files>
go test ./...
go vet ./...
go build ./...
```

Also run `go test -race ./...` after changing concurrent code, network
lifecycle handling, caches, or UDP sessions. A changed package may be tested
first for faster iteration, but all Go changes require a full `go test ./...`
before handoff.

Do not use `./scripts/run.sh` as an automated check. It starts long-lived
listeners and, on Windows/macOS, a tray event loop. Perform a manual smoke test
only with a temporary `PROFILE_PLACE`, a separate configuration, and free
unprivileged ports.

## Change rules

- Target the Go version declared in `go.mod`. Format every changed Go file with
  `gofmt`, and do not reformat unrelated code.
- Preserve rule order: the first matching rule wins, while a matching negation
  excludes the entire current rule. `*.example.com` must continue to include
  the base domain `example.com`.
- Values in `ProxyConfig.Proxies` have contractual meanings: an empty string is
  direct, `#` is blocked, and every other value is an upstream-proxy URL. Do
  not change this behavior in only one layer.
- `ProxyConfig` and `RuleBaseConfig` fields define both the YAML API and the
  generated JSON Schema. When changing them, update defaults, struct tags,
  README documentation, and template/schema tests together.
- Do not load large host lists entirely into memory without a clear need.
  Simple exact and domain-suffix matching uses a compact index and streaming
  parser; glob patterns remain a fallback path.
- Preserve `context.Context` cancellation, deadlines, TCP half-close behavior,
  and connection cleanup on every error path. Do not remove self-connection
  protection from direct or upstream dialing.
- Configuration, caches, logging, and UDP sessions are accessed by multiple
  goroutines. Do not bypass the existing mutexes, `sync.Once`, `sync.Map`, or
  `singleflight`; define ownership and lifecycle explicitly for new shared
  state.
- During reload, a replacement listener must start successfully before the old
  listener is closed. A bind failure must not leave a partially started
  SOCKS5 TCP/UDP listener pair.
- Preserve the build-tag split in `internal/tray`: Linux runs headlessly, while
  Windows/macOS use systray and dialogs. A new platform-specific symbol must
  have implementations for every supported target or a correctly tagged
  fallback.
- Use `t.TempDir`, `httptest`, and loopback listeners in unit tests. Do not
  contact real external DNS/proxy endpoints or write to the user's profile.
- Do not add dependencies or change the `goProxy` module path without a clear
  need. Do not fix unrelated issues opportunistically.

## Generated and local files

Do not commit `goProxy`/`goProxy.exe` binaries, `goProxy.app`, logs, local
`config.yaml`, `goproxy.schema.json`, cache directories, IDE/OS files, or other
runtime profile data. Icons and `FILE_windows.syso` are project source assets;
change them only when required by the task and verify their consumers.

## Definition of done

1. Focused tests have been added or updated for changed behavior.
2. Formatting and the relevant checks from `.agents/verification.md` have been
   completed.
3. The final diff and absence of accidental runtime/generated files have been
   reviewed.
4. The handoff lists changed files, commands run, and any unverified platforms
   or scenarios.
