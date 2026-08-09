# goProxy

goProxy is a cross-platform HTTP/HTTPS and SOCKS5 proxy with ordered,
rule-based routing. Destinations can be reached directly, blocked, or forwarded
through an HTTP, HTTPS, SOCKS5, or SOCKS5h upstream proxy.

It runs as a tray application on Windows and macOS and as a headless process on
Linux. goProxy is intended for localhost and trusted private networks, not for
operation as a public Internet-facing proxy.

## Features

- HTTP proxying, HTTPS via CONNECT, and SOCKS5 TCP/UDP listeners
- A shared routing configuration for HTTP and SOCKS5 clients
- Host glob, IP, CIDR, and DNS-based rules with negation
- Local and remote rule lists, cached remote downloads, and YAML rule files
- Configuration reload without restarting the process
- Optional source-interface binding and a custom DNS resolver
- DNS, CIDR, glob, and routing caches
- Rotating file logs with configurable levels

## Install

Download a prebuilt package from
[GitHub Releases](https://github.com/Feverqwe/goProxy/releases), or build the
project from source with Go 1.25 or newer:

```sh
git clone https://github.com/Feverqwe/goProxy.git
cd goProxy
go build -o goProxy .
```

Platform packaging scripts are also available:

```sh
./scripts/build.sh       # executable for the current Unix-like platform
./scripts/build.mac.sh   # goProxy.app for macOS
```

On Windows, run `scripts\build.win.bat` from a Developer Command Prompt or a
regular command prompt with Go available on `PATH`.

## Quick start

Run the binary with its default profile:

```sh
./goProxy
```

On the first run, goProxy creates a documented `config.yaml` and a
`goproxy.schema.json` file for editor completion and validation. The default
configuration starts:

- an HTTP/HTTPS proxy on `:8080`;
- a SOCKS5 TCP/UDP proxy on `:1080`;
- direct routing for every destination.

Point an HTTP client at `http://127.0.0.1:8080` or a SOCKS5 client at
`socks5://127.0.0.1:1080`.

> [!WARNING]
> The default addresses listen on all network interfaces, and the incoming
> proxies do not authenticate clients. Use the defaults only on a trusted
> private network. For access from the same machine only, bind to loopback
> addresses such as `127.0.0.1:8080` and `127.0.0.1:1080`. Never expose goProxy
> directly to the public Internet.

## Configuration

The default profile directory depends on the platform:

| Platform | Directory |
| --- | --- |
| Windows | Current working directory |
| macOS | `~/Library/Application Support/com.rndnm.goproxy` |
| Linux | Directory containing the executable |

Set `PROFILE_PLACE` to use a different profile directory, or pass an explicit
configuration file:

```sh
./goProxy -config /path/to/config.yaml
```

Relative local rule files are resolved from the directory containing the
configuration file. Relative log files and downloaded-rule caches are stored
in the profile directory.

### Example

```yaml
# yaml-language-server: $schema=./goproxy.schema.json

defaultProxy: direct

proxies:
  direct: ""
  block: "#"
  office: "socks5h://user:password@proxy.example.com:1080"

# Use an empty string to disable either listener.
listenHttpAddr: "127.0.0.1:8080"
listenSocksAddr: "127.0.0.1:1080"

logLevel: info
logFile: goProxy.log
maxLogSize: 10
maxLogFiles: 5

# Refresh remote rules periodically; 0 disables periodic reloads.
autoReloadHours: 24

rules:
  - name: local destinations
    proxy: direct
    hosts: |
      localhost
      *.local
    ips: |
      10.0.0.0/8
      172.16.0.0/12
      192.168.0.0/16

  - name: blocked hosts
    proxy: block
    hosts: |
      ads.example
      *.tracking.example
      ./lists/blocked-hosts.txt

  - name: office proxy
    proxy: office
    hosts: |
      *.example.com
      !status.example.com

# Optional settings for direct connections.
externalIf: ""
externalIp4: ""
externalIp6: ""
externalDns: ""
```

### Routes

`proxies` maps route names to their behavior:

| Value | Behavior |
| --- | --- |
| `""` | Connect directly |
| `"#"` | Block the connection |
| `http://...` or `https://...` | Connect through an HTTP CONNECT proxy |
| `socks5://...` or `socks5h://...` | Connect through a SOCKS5 proxy |

Upstream URLs may include credentials, for example
`http://user:password@proxy.example.com:3128`. HTTP and HTTPS upstreams use
Basic proxy authentication; SOCKS5 upstreams use username/password
authentication when credentials are present.

`defaultProxy` and every rule's `proxy` value must name an entry in `proxies`.

### Rules

Rules are evaluated from top to bottom. The first matching rule selects its
route; if no rule matches, `defaultProxy` is used.

A rule matches when at least one positive entry in `hosts` or `ips` matches.
Any matching entry prefixed with `!` excludes the entire rule, so evaluation
continues with the next rule. For example, this sends every host under
`example.com` except `status.example.com` through `office`:

```yaml
- name: office sites
  proxy: office
  hosts: |
    *.example.com
    !status.example.com
```

Host entries support exact names and glob patterns. `*.example.com` also
matches the base domain `example.com`. IP entries accept individual addresses,
CIDRs, and hostnames that are resolved before comparison.

Entries may be separated by whitespace, commas, or newlines. Lines whose first
non-space characters are `#` or `//` are comments.

### External rules

The `hosts` and `ips` fields can include local list files or HTTP(S) URLs:

```yaml
hosts: |
  ./lists/hosts.txt
  https://example.com/rules/hosts.txt
ips: |
  ./lists/networks.txt
```

Use `./` for a relative list path or an absolute path. A leading `!` applies
negation to every entry loaded from that source.

For a structured rule, use `externalRule` with a local YAML file or URL:

```yaml
rules:
  - name: company networks
    proxy: office
    externalRule: ./rules/company.yaml
```

The external file may contain `name`, `hosts`, and `ips`:

```yaml
name: shared company rules
hosts: |
  *.corp.example
ips: |
  10.20.0.0/16
```

Inline and external `hosts`/`ips` entries are combined. An inline `name` takes
precedence over the external name.

Remote files are cached under the profile directory. If an update fails,
goProxy continues with the cached copy when one is available.

### Settings reference

| Setting | Description |
| --- | --- |
| `defaultProxy` | Route used when no rule matches |
| `proxies` | Named direct, blocked, or upstream routes |
| `listenHttpAddr` | HTTP/HTTPS listen address; empty disables it |
| `listenSocksAddr` | SOCKS5 TCP/UDP listen address; empty disables it |
| `logLevel` | `debug`, `info`, `warn`, `error`, or `none` |
| `logFile` | Rotating log path; empty disables file logging |
| `maxLogSize` | Maximum log size in MB before rotation |
| `maxLogFiles` | Number of rotated files to retain |
| `autoReloadHours` | Remote-rule reload interval; `0` disables it |
| `rules` | Ordered routing rules |
| `externalIf` | Interface used to detect direct-connection source IPs |
| `externalIp4` | Explicit IPv4 source address for direct connections |
| `externalIp6` | Explicit IPv6 source address for direct connections |
| `externalDns` | DNS server for direct connections; port 53 is the default |

The generated `goproxy.schema.json` is the authoritative field reference and
contains types, defaults, constraints, and editor descriptions.

## Reload and lifecycle

On Windows and macOS, the tray menu provides:

- **Reload config** — apply configuration changes;
- **Reload rules** — apply the configuration and force remote rule downloads;
- **Open config directory**;
- **Check updates**;
- **Quit**.

On Unix-like systems, send `SIGHUP` to reload the configuration:

```sh
kill -HUP <pid>
```

Set `autoReloadHours` to periodically refresh remote rules. `SIGINT` and
`SIGTERM` shut the process down gracefully.

Listener addresses are reloadable. If an address changes, goProxy opens the
replacement listener before closing the old one.

## Protocol notes

- HTTP clients can send plain HTTP requests and HTTPS tunnels through CONNECT.
- SOCKS5 supports TCP CONNECT and UDP ASSOCIATE.
- SOCKS5 UDP can use a direct route or a SOCKS5/SOCKS5h upstream. A route that
  points to an HTTP/HTTPS upstream falls back to a direct UDP connection.
- `externalIf`, `externalIp4`, `externalIp6`, and `externalDns` affect direct
  connections, including direct SOCKS5 UDP traffic.

## Command line

```text
goProxy [-config path] [-version]
```

| Option | Description |
| --- | --- |
| `-config path` | Use the specified YAML configuration file |
| `-version` | Print the version and exit |

## Development

Run the standard checks from the repository root:

```sh
go test ./...
go vet ./...
go build ./...
```

## License

goProxy is available under the [MIT License](LICENSE).
