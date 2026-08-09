# goProxy architecture

## Startup and reload flow

`main.go` resolves the profile/configuration path, creates a shared
`CacheManager`, loads `ProxyConfig`, and constructs one `ProxyHandler`. The HTTP
listener and the TCP/UDP sides of SOCKS5 are started through the owners in
`proxy_servers.go`.

OS signals, tray actions, and `TickerManager` feed a single reload loop. A
reload creates a new configuration, replaces listeners when necessary, and
then updates the routing state inside `ProxyHandler` under its lock.
`configMutex` protects configuration transitions, while `ProxyHandler.mu`
protects concurrent request access to the current `ProxyDecision`.

Critical lifecycle invariants:

- a replacement listener is opened before the old listener is closed;
- if opening the SOCKS5 UDP listener fails, the already-open TCP listener is
  closed;
- server `Close` methods are idempotent through `sync.Once`;
- normal listener shutdown is not reported as a server failure;
- reload and shutdown must not leave background listeners or connections
  behind.

## Configuration and rules

`internal/config.ProxyConfig` is the external YAML contract. On first launch,
`saveDefaultConfig` creates a documented `config.yaml` and
`goproxy.schema.json`. Schema metadata comes from `config_*` struct tags, and
default values come from `defaultConfig`.

`preParseRuleLists` combines inline rules with `externalRule`. Local paths are
resolved relative to the configuration directory, while URL sources are stored
in the profile cache. For host lists, `scanRuleTokens` parses the input as a
stream, after which `hostRuleBuilder` separates entries into:

- exact hosts;
- domain suffixes for `*.domain`, including the base domain itself;
- fallback glob patterns;
- separate positive and negative sets.

Rules are evaluated from top to bottom in `ProxyDecision.evaluateRules`. A
negative match skips the entire current rule. If a host condition does not
determine the route, IP/CIDR conditions use the DNS cache. The first matching
rule is resolved through `ProxyConfig.Proxies`: `""` means direct, `#` means
blocked, and a URL means upstream. `defaultProxy` is used when nothing matches.

## Incoming protocols

HTTP and HTTPS CONNECT requests pass through `ProxyHandler` and
`elazarl/goproxy`. The routing result is passed to the transport dialer through
the request context so that `HTTP_PROXY`/`HTTPS_PROXY` environment variables
cannot bypass project routing rules.

SOCKS5 accepts TCP and UDP on the same address. `SocksHandler` uses the same
`ProxyHandler`/`ProxyDecision`, so direct, blocked, and upstream behavior must
remain consistent between both incoming protocols. `UDPSessionManager`
deduplicates upstream-session creation, updates activity timestamps, and closes
expired connections.

## Outgoing connections

`ProxyHandler.dialContext` implements three branches:

- direct connections through `directDialer`;
- SOCKS5/SOCKS5h upstream connections with a handshake over an already-open
  TCP connection;
- HTTP/HTTPS upstream connections through CONNECT, including Basic
  authentication and TLS to HTTPS proxies.

Direct dialing honors `externalDns`, `externalIp4`, `externalIp6`, the shared
timeout, and Happy Eyeballs behavior between IPv4 and IPv6.
`selfConnectionGuard` prevents recursive connections to the application's own
listeners and must remain active for any new dialing path.

When changing TCP stream copying, preserve half-close behavior: completion of
writes in one direction must not prematurely stop reads in the other.

## Caches and concurrency

`internal/cache.CacheManager` owns DNS, CIDR, and glob caches. DNS lookups are
coalesced with `singleflight`; a custom DNS key must include both the server and
source binding, or requests from different network configurations could share
an invalid result. Returned IP slices are cloned so callers cannot mutate a
cache entry.

Routing uses separate LRUs for host-dependent and IP-dependent decisions; the
IP cache follows the DNS resolution TTL. Rule changes create a new
`ProxyDecision` instead of reusing decisions from the previous configuration.

## Platforms and profiles

The `PROFILE_PLACE` environment variable overrides the profile path. By
default, Windows uses the working directory, macOS uses
`~/Library/Application Support/com.rndnm.goproxy`, and Linux uses the binary's
directory. Configuration, schema, cache, and file logs live in the profile or
beside an explicitly selected configuration, according to their existing path
helpers.

Files matching `internal/tray/*_linux.go` provide the headless lifecycle. Files
tagged `!linux` use systray, dialogs, and browser opening. macOS packaging lives
in `scripts/build.mac.sh` and `simple_appify.sh`; Windows packaging lives in
`scripts/build.win.bat`. Linker version values come from
`scripts/_variables.sh` or the release environment.
