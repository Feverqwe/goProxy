# Configuration package instructions

## Scope and responsibilities

This package owns the YAML model, profile paths, external rule retrieval and
cache files, and conversion of rule text into host/IP tokens. Keep network
transport policy out of this package; routed downloads receive an
`HTTPClientFunc` from the handler layer.

The load sequence is:

1. Decode the main YAML file or create the default configuration.
2. Attach runtime-only cache, path, and logger fields.
3. Reconfigure logging and detect optional interface addresses.
4. Load and parse local/cached external rules.
5. Precompile host glob and IP/CIDR patterns.

Changes to reload behavior must preserve a usable previous configuration when
the new configuration cannot be loaded completely.

## Rule parsing invariants

- Entries may be separated by whitespace or commas.
- `!` negates a host/IP token.
- With host wildcard expansion enabled, `*.example.com` also produces
  `example.com`; preserve the `!` prefix on the expanded base domain.
- `#` and `//` start a comment only at the beginning of a line or after
  whitespace. They must not truncate `https://...`, `path//segment`, or URL
  fragments such as `list#section`.
- A token beginning with `http://`, `https://`, `/`, or `./` is an external or
  local rule source, not a literal match pattern.
- Rule order remains significant even though external sources are prepared
  concurrently.

Add table-driven parser tests when changing token, comment, wildcard,
negation, or external-source syntax.

## External rules and cache

- Initial process startup intentionally uses `cacheOnly=true` for remote
  sources. Do not add startup downloads without an explicit product change.
- Reload downloads must use the supplied routed HTTP client when available.
- A download failure may fall back to the last valid cache file.
- Concurrent references to the same URL must not corrupt or race on its cache
  file.
- Cache replacement must not remove the last valid file before the replacement
  is safely persisted.
- Avoid unbounded test downloads. Use `httptest.Server` and temporary profile
  directories for cache tests.

When adding YAML fields, update `ProxyConfig`, defaults where appropriate,
README documentation, and load/reload tests together.
