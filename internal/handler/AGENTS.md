# Proxy handler package instructions

## Routing and transport invariants

This package implements rule decisions and all HTTP, SOCKS5, TCP, and UDP data
paths. Preserve the following invariants:

- A proxy URL of `""` means direct and `"#"` means blocked. Other values must
  have an explicitly supported scheme.
- `http.Transport.Proxy` stays disabled and goproxy's environment-derived
  `ConnectDial` stays unset. Route selection is passed through request context
  to `ProxyHandler.dialContext`.
- An `https://` upstream proxy uses real TLS with certificate and hostname
  verification before sending CONNECT. Do not downgrade it to plaintext.
- Direct connections using custom DNS/source addresses must pair IPv4 targets
  with the configured IPv4 source and IPv6 targets with the IPv6 source.
- Host matching is normalized for surrounding whitespace, case, and a trailing
  root dot.
- Decision and DNS caches are bounded expirable LRUs. Keep routing cache expiry
  aligned with DNS freshness unless a change is explicitly designed otherwise.

Configuration swaps and shared transports are concurrent with live requests.
Access the current decision under `ProxyHandler.mu`, and account for cached
idle connections or long-lived UDP sessions when route configuration changes.

## TCP and SOCKS5

- SOCKS5 upstream handshakes must honor context cancellation and the explicit
  timeout; avoid APIs that can block indefinitely during negotiation.
- TCP tunnels preserve half-close semantics. EOF in one direction should call
  `CloseWrite` on the opposite side and allow the response direction to drain.
- Wrappers such as `bufferedConn` and `halfCloseConn` must continue exposing
  `CloseRead`/`CloseWrite` where the underlying connection supports them.
- On dial failure, send the appropriate SOCKS5 failure reply before returning.

## UDP sessions

- Session creation for one client/target key must be atomic. Losing candidate
  connections must be closed.
- `lastActive`, closed state, listener startup, and session deletion are shared
  state and must remain race-free.
- Delete by key and expected session so an old listener cannot remove a newer
  replacement session.
- Per-packet deadlines must not become permanent idle deadlines that kill an
  otherwise active association.
- Only SOCKS5 upstreams support proxied UDP. Falling back to direct for other
  selected upstream schemes is intentional product behavior.
- Any new manager goroutine or ticker needs an explicit shutdown path owned by
  the SOCKS server lifecycle.

## Verification

Add focused tests for every transport or synchronization change, then run:

```sh
go test -race ./internal/handler
go test -race ./...
go vet ./...
```

Use loopback listeners and deadlines in protocol tests so failures terminate
quickly and do not depend on external connectivity.
