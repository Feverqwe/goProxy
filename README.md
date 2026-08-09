# 🚀 GoProxy - Advanced HTTP/HTTPS/SOCKS5 Proxy Server

**Intelligent proxy server with rule-based routing, caching, and system tray integration**

---

## 📋 Overview

GoProxy is a powerful proxy server written in Go that provides intelligent routing, caching, and system tray integration. It supports HTTP, HTTPS, and SOCKS5 protocols with rule-based routing and hot-reloadable configuration.

## ✨ Features

### 🌐 Protocols & Servers
- **Multi-Protocol Support**: HTTP, HTTPS, and SOCKS5 proxy protocols
- **Dual Server Mode**: Simultaneous HTTP/HTTPS and SOCKS5 server support
- **UDP Support**: SOCKS5 UDP association for gaming and VoIP

### 🎯 Routing & Rules
- **Rule-Based Routing**: Advanced pattern matching for IPs and hosts with wildcard support
- **External Rule Sources**: Load rules from URLs and local files with automatic caching
- **Negation Support**: Exclude specific patterns using `!` prefix
- **Blocking Support**: Block specific domains/URLs

### ⚡ Performance
- **Caching**: DNS, pattern, and CIDR caching for maximum performance
- **Connection Pooling**: Efficient connection reuse and management
- **Hot Reload**: Configuration reload without restart

### 🖥️ Interface & Logging
- **System Tray Integration**: Native system tray support (Windows/macOS)
- **Logging**: Configurable logging with file rotation
- **Cross-Platform**: Windows, macOS, and Linux support

### 🔧 Advanced Features
- **External Interface Binding**: Bind connections to specific network interfaces or IP addresses
- **Custom DNS Resolution**: Configurable DNS servers with source IP binding
- **Auto-Update**: Periodic configuration reload

## 📦 Installation

### Pre-built Binaries

Download the latest release from the [Releases page](https://github.com/Feverqwe/goProxy/releases):

| Platform | File | Description |
|----------|------|-------------|
| 🪟 **Windows** | `goProxy.exe` | Console application with system tray |
| 🍎 **macOS** | `GoProxy.app` | Native macOS application bundle |
| 🐧 **Linux** | `goProxy` | Command-line binary |

### Building from Source

#### Prerequisites
- Go 1.25.0 or later
- Git

#### Build Steps

1. **Clone the repository**
   ```bash
   git clone https://github.com/Feverqwe/goProxy.git
   cd goProxy
   ```

2. **Build for your platform**

   **Linux/Unix:**
   ```bash
   chmod +x ./scripts/*.sh
   bash ./scripts/build.sh
   ```

   **macOS:**
   ```bash
   chmod +x ./scripts/*.sh
   sh ./scripts/build.mac.sh
   ```

   **Windows:**
   ```cmd
   cd scripts
   build.win.bat
   ```

## ⚙️ Configuration

### Configuration File Location

GoProxy automatically creates a configuration file on first run:

| Platform | Path |
|----------|------|
| 🪟 Windows | Current working directory |
| 🍎 macOS | `~/Library/Application Support/com.rndnm.goproxy/config.yaml` |
| 🐧 Linux | Same directory as the executable |

On first run GoProxy also creates `goproxy.schema.json` next to the configuration
file. The generated YAML references this schema, so editors with YAML Language
Server support provide field descriptions, autocompletion, and validation. The
configuration itself contains comments for optional settings, while the schema
also provides constraints and examples.

### Example Configuration

```yaml
# Basic settings
defaultProxy: "direct"

# Proxy definitions
proxies:
  socks5: "socks5://localhost:1080"
  http: "http://localhost:8081"
  direct: ""
  block: "#"

# Server settings
listenHttpAddr: ":8080"
listenSocksAddr: ":1080"

# Logging
logLevel: "info"
logFile: "goProxy.log"
maxLogSize: 10
maxLogFiles: 5

# Auto-reload
autoReloadHours: 24

# Routing rules
rules:
  # Rule 1: Direct connection for local networks
  - name: "Local Networks"
    proxy: "direct"
    ips: "192.168.1.0/24 10.0.0.0/8 172.16.0.0/12"
    hosts: "localhost *.local *.example.com"

  # Rule 2: Block domains using external DNS blocklists
  - name: "dns-blocklist-pro"
    proxy: "block"
    hosts: "https://cdn.jsdelivr.net/gh/hagezi/dns-blocklists@latest/wildcard/pro.txt"

  # Rule 3: Use HTTP proxy for external domains
  - name: "External Domains"
    proxy: "http"
    hosts: "*.external.com api.*.com"

  # Rule 4: Load rules from external file
  - name: "External Rule Configuration"
    proxy: "socks5"
    externalRule: "./external-rules.yaml"

# External interface and DNS configuration
externalIf: "eth0"      # Auto-detect IPs from this interface
externalIp4: ""         # Force IPv4 source address
externalIp6: ""         # Force IPv6 source address
externalDns: "8.8.8.8"  # Custom DNS server
```

### 📝 Configuration Parameters

#### Global Settings

| Parameter | Type | Description |
|-----------|------|-------------|
| `defaultProxy` | string | Default proxy when no rules match |
| `proxies` | map | Map of proxy definitions |
| `listenHttpAddr` | string | HTTP/HTTPS server address and port (e.g., ":8080") |
| `listenSocksAddr` | string | SOCKS5 server address and port (e.g., ":1080") |
| `logLevel` | string | Logging level: `debug`, `info`, `warn`, `error`, `none` |
| `logFile` | string | Log file path (relative to config directory) |
| `maxLogSize` | int | Maximum log file size in MB before rotation |
| `maxLogFiles` | int | Number of backup log files to keep |
| `autoReloadHours` | int | Automatic reload interval in hours (0 = disabled) |
| `externalIf` | string | Network interface name for auto-detecting source IPs |
| `externalIp4` | string | Force IPv4 source address for direct connections |
| `externalIp6` | string | Force IPv6 source address for direct connections |
| `externalDns` | string | Custom DNS server (IP:port format) |

#### Proxy Definitions

- `direct`: No proxy (direct connection)
- `block`: Block the connection entirely
- Custom proxies: HTTP/HTTPS/SOCKS5 URLs

**Examples:**
```yaml
proxies:
  direct: ""
  block: "#"
  my_socks: "socks5://user:pass@proxy.example.com:1080"
  my_http: "http://proxy.example.com:8080"
```

#### Rule Configuration

Rules are evaluated in order. Each rule can match based on:

| Field | Description |
|-------|-------------|
| `name` | Optional descriptive name for the rule (used in logging) |
| `proxy` | Which proxy to use for this rule |
| `ips` | CIDR notation, IP addresses, or URLs to external IP lists |
| `hosts` | Hostname patterns with wildcards, or URLs to external host lists |
| `externalRule` | External YAML file containing rule configuration |

### 🎯 Supported Rule Types

#### IP Rules (`ips` field)

```yaml
ips: |
  192.168.1.1              # Individual IP
  192.168.1.0/24           # CIDR notation
  example.com              # Domain name (resolved to IPs)
  https://example.com/ips.txt  # External URL
  ./ips.txt                # Local file
  !192.168.1.100           # Negation (exclude)
```

#### Host Rules (`hosts` field)

```yaml
hosts: |
  example.com              # Exact domain
  *.example.com            # Wildcard (includes base domain)
  https://example.com/hosts.txt  # External URL
  ./hosts.txt              # Local file
  !blocked-domain.com      # Negation (exclude)
```

**Note:** `*.example.com` automatically includes `example.com`

#### External Rule Files (`externalRule` field)

```yaml
rules:
  - name: "My External Rules"
    proxy: "socks5"
    externalRule: "./external-rules.yaml"
```

**Example external-rules.yaml:**
```yaml
name: "External Rule Set"
ips: "192.168.100.0/24 10.100.0.0/16"
hosts: "*.external.com api.external.com"
```

## 🚀 Usage

### Command Line

```bash
# Basic usage (uses default config location)
./goProxy

# Specify custom config file
./goProxy -config /path/to/config.yaml

# Show version information
./goProxy -version
```

### 🖥️ System Tray (Windows/macOS)

When running on Windows or macOS, GoProxy provides a system tray icon with options:

- **Reload config** — Reload configuration without restarting
- **Open config directory** — Open the directory containing the config file
- **Quit** — Gracefully shut down the proxy

> **Note:** System tray is not available on Linux. Use command-line signals instead.

### 🔄 Hot Reload

Configuration can be reloaded without restarting the server:

| Platform | Method |
|----------|--------|
| Windows/macOS | Use "Reload config" option in system tray |
| All platforms | Send SIGHUP signal: `kill -HUP <pid>` |
| Automatic | Configure `autoReloadHours` in config (0 = disabled) |

## 📚 Documentation

### Rule Matching Logic

GoProxy uses sophisticated pattern matching with intelligent caching:

#### 🌐 Host Matching
- Supports wildcards: `*.example.com` matches both `sub.example.com` and `example.com`
- Automatically handles ports: `example.com:8080` is properly parsed
- Cached pattern matching for performance

#### 🔢 IP Matching
- CIDR notation: `192.168.1.0/24`
- Individual IPs: `192.168.1.1`
- Domain resolution: Hostnames are resolved to IPs with DNS caching

#### ⚙️ Rule Evaluation Order

1. Rules are processed in order from top to bottom
2. For each rule:
   - External rule files (`externalRule`) are loaded first
   - Fields from external rule are merged with main rule
   - External rule lists (URLs in `ips` and `hosts` fields) are loaded and parsed
3. Matching is attempted in this order: Host → IP
4. First matching rule determines the proxy to use
5. If no rules match, the `defaultProxy` is used
6. Negated patterns (`!pattern`) exclude matches from the rule

### 🔗 External Rule Merging

When using `externalRule`, fields are merged as follows:

- **Ips, Hosts**: Concatenated with newlines, then parsed together
- **Name**: Uses main rule name if specified, otherwise external rule name

This allows you to create modular rule configurations and combine multiple rule sources.

### 💾 External Rule Caching

- External rules from URLs are automatically cached locally
- Cache files are stored in platform-specific cache directories
- Failed downloads fall back to cached versions when available
- Cache files are named using SHA-256 hashes of the URL for uniqueness

### 🔍 Rule Parsing Logic

- Supports comments in rule lists using `//` or `#`
- Multiple entries can be separated by commas or spaces
- Wildcard domains (`*.example.com`) are automatically expanded to include the base domain
- External rule sources are loaded and merged with local rules

### 🌍 Source IP Binding

- Configure `externalIf` to auto-detect IPs from a network interface
- Use `externalIp4` and `externalIp6` to force specific source IPs
- Custom DNS resolution with source IP binding for direct connections

## 📦 Dependencies

| Library | Purpose |
|---------|---------|
| [`github.com/elazarl/goproxy`](https://github.com/elazarl/goproxy) | Core HTTP/HTTPS proxy functionality |
| [`github.com/getlantern/systray`](https://github.com/getlantern/systray) | System tray integration |
| [`github.com/gobwas/glob`](https://github.com/gobwas/glob) | Pattern matching |
| [`github.com/hashicorp/golang-lru/v2`](https://github.com/hashicorp/golang-lru/v2) | LRU caching |
| [`github.com/txthinking/socks5`](https://github.com/txthinking/socks5) | SOCKS5 server implementation |
| [`gopkg.in/yaml.v3`](https://github.com/go-yaml/yaml) | YAML configuration parsing |
| [`gopkg.in/natefinch/lumberjack.v2`](https://github.com/natefinch/lumberjack) | Log file rotation |
| [`github.com/skratchdot/open-golang`](https://github.com/skratchdot/open-golang) | Opening config directory |
| [`golang.org/x/net`](https://pkg.go.dev/golang.org/x/net) | Extended networking capabilities |
| [`golang.org/x/sync`](https://pkg.go.dev/golang.org/x/sync) | Synchronization primitives |

## 🤝 Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## 📄 License

This project is licensed under the MIT License. See the [LICENSE](LICENSE) file for details.
