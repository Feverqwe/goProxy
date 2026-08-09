# Verifying changes

## Baseline checks

Run the following commands from the repository root:

```sh
gofmt -w <changed-go-files>
go test ./...
go vet ./...
go build ./...
```

Apply `gofmt` only to changed Go files. Documentation-only changes need only a
review of the diff, links, and documented commands; running the Go suite is not
required.

## Checks by area

- Routing, host indexes, and rule parsing:
  `go test ./internal/config ./internal/handler`.
- DNS, custom resolvers, or cache keys: `go test ./internal/cache` plus the
  relevant direct-dial tests in `internal/handler`.
- HTTP/SOCKS listener lifecycle: `go test . ./internal/handler`.
- UDP sessions, goroutines, mutexes, channels, reload, or logging:
  `go test -race ./...` after the ordinary test suite.
- Configuration/schema generation: `go test ./internal/config`; confirm that
  every external YAML field appears in the schema and has the correct default.
- Build tags/tray: run a build or compile check on the current platform and,
  when supported by the toolchain, on the affected target platform. Clearly
  distinguish executed targets from compile-only targets in the handoff.

Prefer table-driven tests beside the changed code. Network tests must use
`127.0.0.1:0`, `httptest.Server`, short deadlines, and mandatory cleanup.
Concurrency tests must not rely on sleeps as their only synchronization
mechanism.

## Runtime isolation

Do not run `./scripts/run.sh` or a built binary without a clear need. The
application occupies HTTP/SOCKS ports, may open a tray, and creates profile
files.

If a manual smoke test is required:

1. Create a temporary directory outside the repository.
2. Point `PROFILE_PLACE` to it, or pass `-config` with a path inside it.
3. Use free loopback ports; do not use production upstream proxies or external
   rule URLs.
4. Stop the process with a normal signal and verify that both listeners close.
5. Remove only the temporary directory created for the test.

Do not use the user's `config.yaml`, cache, or logs as fixtures. Use a local
`httptest.Server` for external rule sources; tests must be reproducible without
internet access.

## Builds and releases

`./scripts/build.sh` creates the ignored `goProxy` binary and injects the
version through linker flags. `./scripts/build.mac.sh` also recreates
`GoProxy.app`; the Windows script creates a GUI binary. These commands are
appropriate after changing packaging, resources, or release scripts, while an
ordinary unit-level change only requires `go build ./...`.

Do not run `scripts/release.sh` for verification. It changes the version,
creates a commit and tag, and pushes them.

Before handoff, run `git diff --check` and `git status --short`. Report the
commands and results, along with any skipped check and the reason it was
skipped.
