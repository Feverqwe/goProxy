# Repository instructions for agents

## Scope

These instructions apply to the whole repository. More specific `AGENTS.md`
files under `internal/config` and `internal/handler` add rules for those
packages.

## Project overview

GoProxy is a Go 1.25 application that exposes HTTP/HTTPS and SOCKS5 proxy
servers. Routing is selected by ordered host/IP rules and can resolve to a
direct connection, a named upstream proxy, or a block decision. The process
also owns configuration reload, external rule caching, logging, and a
platform-specific system tray.

Important locations:

- `main.go`: process lifecycle, listeners, signals, reloads, and tray wiring.
- `internal/config`: YAML loading, rule parsing, external lists, and disk cache.
- `internal/handler`: routing decisions and HTTP/SOCKS/TCP/UDP transport.
- `internal/cache`: DNS, glob, and CIDR caches.
- `internal/logging`, `internal/ticker`, `internal/tray`: supporting services.
- `scripts`: release and platform packaging scripts.

## Commands

Run commands from the repository root.

```sh
gofmt -w <changed-go-files>
go test ./...
go test -race ./...
go vet ./...
go build ./...
git diff --check
```

Some handler tests open loopback TCP listeners. In a restricted sandbox they
may need explicit permission for local networking; a bind denial is an
environment failure, not a reason to weaken or skip the test.

For release-shaped local builds use the scripts already in the repository:

```sh
sh ./scripts/build.sh
sh ./scripts/build.mac.sh   # macOS app bundle
```

Do not change `scripts/_variables.sh`, create tags, or run release scripts
unless the task explicitly concerns versioning or a release.

## Change guidelines

- Keep changes focused and preserve unrelated work in the worktree.
- Follow normal Go conventions and run `gofmt`; do not edit `go.sum` by hand.
- Add or update tests for routing, protocol, parsing, caching, and concurrency
  behavior. Prefer local deterministic test servers over public network calls.
- Use `t.TempDir` and scoped environment variables for filesystem/profile
  tests. Tests must not write to a developer's real profile or configuration.
- Preserve platform build separation in tray files and verify affected build
  tags when changing platform code.
- Keep README configuration examples and parameter documentation synchronized
  with user-visible YAML or rule syntax changes.
- Treat networking changes as concurrency-sensitive. Verify ownership and
  shutdown of connections, goroutines, timers, and mutable shared state.

## Intentional product behavior

Do not change these choices unless the task explicitly asks for it:

- First startup loads remote rule sources from an existing cache only. This is
  intentional so the proxy can start before it has a route for downloading
  those rules.
- SOCKS5 UDP can use an upstream SOCKS5 proxy. If a selected upstream uses a
  different scheme, UDP falls back to direct transport.
- The listeners currently run without client authentication and may bind all
  interfaces, depending on their configured addresses.
- Proxy map values use `""` for direct routing and `"#"` for blocking.

Do not silently restore environment-derived proxy behavior. Outbound routing
must remain controlled by the loaded GoProxy configuration.
