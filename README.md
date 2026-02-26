# GoProxy - Advanced HTTP/HTTPS/SOCKS5 Proxy Server

GoProxy is a sophisticated proxy server written in Go that provides intelligent routing, caching, and system tray integration. It supports HTTP, HTTPS, and SOCKS5 protocols with rule-based routing and hot-reloadable configuration.

## Features

- **Multi-Protocol Support**: HTTP, HTTPS, and SOCKS5 proxy support with authentication
- **Dual Server Mode**: Simultaneous HTTP/HTTPS and SOCKS5 server support
- **Rule-Based Routing**: Advanced pattern matching for IPs, hosts, and URLs
- **Hot Reload**: Configuration reload without restart (SIGHUP, tray menu, or periodic)
- **System Tray Integration**: Native system tray support (Windows/macOS)
- **Caching**: DNS, pattern, and CIDR caching for performance
- **Logging**: Configurable logging with file rotation
- **Cross-Platform**: Windows, macOS, and Linux support
- **Blocking Support**: Ability to block specific domains/URLs
- **External Rule Sources**: Support for loading rules from URLs and local files
- **Inverted Rules**: Support for "not" logic to match everything except specified patterns
- **External Interface Binding**: Bind connections to specific network interfaces or IP addresses
- **Custom DNS Resolution**: Configurable DNS servers with source IP binding
- **UDP Support**: SOCKS5 UDP association support
- **Connection Pooling**: Efficient connection reuse and management

## Installation

### Pre-built Binaries

Download the latest release from the [Releases page](https://github.com/Feverqwe/goProxy/releases) for your platform:

- **Windows**: `goProxy.exe` (console application with system tray)
- **macOS**: `GoProxy.app` (native macOS application bundle)
- **Linux**: `goProxy` (command-line binary)

### Building from Source

1. **Prerequisites**: Go 1.25.0 or later
2. **Clone the repository**:
   ```bash
   git clone https://github.com/Feverqwe/goProxy.git
   cd goProxy
   ```
3. **Build for your platform**:

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

## Configuration

GoProxy uses a YAML configuration file that is automatically created on first run. The configuration file location depends on your platform:

- **Windows**: Current working directory
- **macOS**: `~/Library/Application Support/com.rndnm.goproxy/config.yaml`
- **Linux**: Same directory as the executable

### Example Configuration

```yaml
# Example configuration file for goProxy with string-based ips, hosts, and urls
# Elements can be separated by commas or spaces

defaultProxy: "direct"

proxies:
  socks5: "socks5://localhost:1080"
  http: "http://localhost:8081"
  direct: ""
  block: "#"

listenHttpAddr: ":8080"
listenSocksAddr: ":1080"
logLevel: "info"
logFile: "goProxy.log"
maxLogSize: 10
maxLogFiles: 5
autoReloadHours: 24

rules:
  # Rule 1: Direct connection for local networks and internal domains
  - name: "Local Networks"
    proxy: "direct"
    ips: "192.168.1.0/24 10.0.0.0/8 172.16.0.0/12"
    hosts: "localhost *.local *.example.com internal.company.com"

  # Rule 2: Block domains using external DNS blocklists
  - proxy: "block"
    name: "dns-blocklist-pro"
    externalHosts: https://cdn.jsdelivr.net/gh/hagezi/dns-blocklists@latest/wildcard/pro.txt

  # Rule 3: Block domains using external DNS blocklists
  - proxy: "block"
    name: "dns-blocklist-tif"
    externalHosts: https://cdn.jsdelivr.net/gh/hagezi/dns-blocklists@latest/wildcard/tif.txt

  # Rule 4: Use HTTP proxy for other external domains
  - name: "External Domains"
    proxy: "http"
    hosts: "*.external.com api.*.com"

  # Rule 5: Load complete rule configuration from external YAML file
  - name: "External Rule Configuration"
    proxy: "http"
    externalRule: "./external-rules.yaml"
    hosts: "additional-host.com"  # Will be merged with external rule hosts

# External interface and DNS configuration for source IP binding
externalIf: "eth0"      # Auto-detect IPs from this interface
externalIp4: ""         # Force IPv4 source address
externalIp6: ""         # Force IPv6 source address
externalDns: "8.8.8.8"  # Custom DNS server for resolution
```

### Configuration Options

#### Global Settings
- `defaultProxy`: Default proxy to use when no rules match
- `proxies`: Map of proxy definitions
- `listenHttpAddr`: HTTP/HTTPS proxy server address and port (e.g., ":8080")
- `listenSocksAddr`: SOCKS5 server address and port (e.g., ":1080")
- `logLevel`: Logging level (debug, info, warn, error, none)
- `logFile`: Log file path (relative to config directory)
- `maxLogSize`: Maximum log file size in MB before rotation
- `maxLogFiles`: Number of backup log files to keep
- `autoReloadHours`: Automatic configuration reload interval in hours (0 to disable)
- `externalIf`: Network interface name for auto-detecting source IPs
- `externalIp4`: Force IPv4 source address for direct connections
- `externalIp6`: Force IPv6 source address for direct connections
- `externalDns`: Custom DNS server for host resolution (IP:port format)

#### Proxy Definitions
- `direct`: No proxy (direct connection)
- `block`: Block the connection entirely
- Custom proxies: HTTP/HTTPS/SOCKS5 URLs

#### Rule Configuration
Rules are evaluated in order. Each rule can match based on:
- `name`: Optional descriptive name for the rule (used in logging)
- `ips`: CIDR notation or IP addresses
- `hosts`: Hostname patterns with wildcards (`*.example.com`)
- `externalIps`: External sources for IP rules (URLs or local file paths)
- `externalHosts`: External sources for host rules (URLs or local file paths)
- `externalRule`: External YAML file containing rule configuration (without Proxy field)
- `not`: Invert the rule logic (match everything EXCEPT the patterns)

#### External Rule Sources
GoProxy supports loading rules from external sources:

**Individual Rule Lists:**
- **URLs**: HTTP/HTTPS endpoints (automatically cached)
- **Local files**: Relative to config directory or absolute paths
- **Caching**: External rules are cached locally for performance
- **Fallback**: Uses cached version if external source is unavailable

**Complete Rule Configuration (ExternalRule):**
- **YAML files**: Load complete rule configuration from external YAML files
- **Field merging**: Fields from external rule are merged with main rule
- **Relative paths**: Files are resolved relative to main config directory
- **No Proxy field**: External rule files use `RuleBaseConfig` (without Proxy field)

Example external rule file (`external-rules.yaml`):
```yaml
name: "External Rule Set"
ips: "192.168.100.0/24 10.100.0.0/16"
hosts: "*.external.com api.external.com"
externalIps: "http://example.com/ips.txt"
externalHosts: "http://example.com/hosts.txt"
not: false
```

Fields are merged by concatenating with newlines, allowing you to combine local and external rules.

## Usage

### Command Line

```bash
# Basic usage (uses default config location)
./goProxy

# Specify custom config file
./goProxy -config /path/to/config.yaml

# Show version information
./goProxy -version
```

### System Tray (Windows/macOS)

When running on Windows or macOS, GoProxy provides a system tray icon with the following options:
- **Reload config**: Reload configuration without restarting
- **Open config directory**: Open the directory containing the config file
- **Quit**: Gracefully shut down the proxy

### Hot Reload

Configuration can be reloaded without restarting the server:
- **Windows/macOS**: Use the "Reload config" option in the system tray
- **All platforms**: Send SIGHUP signal: `kill -HUP <pid>`

## Rule Matching Logic

GoProxy uses sophisticated pattern matching with intelligent caching:

### Host Matching
- Supports wildcards: `*.example.com` matches both `sub.example.com` and `example.com`
- Automatically handles ports: `example.com:8080` is properly parsed
- Cached pattern matching for performance

### IP Matching
- CIDR notation: `192.168.1.0/24`
- Individual IPs: `192.168.1.1`
- Domain resolution: Hostnames are resolved to IPs for matching with DNS caching


### Rule Evaluation
1. Rules are processed in order from top to bottom
2. For each rule:
   - External rule files (`externalRule`) are loaded first
   - Fields from external rule are merged with main rule (Ips, Hosts, URLs, etc.)
   - External rule lists (`externalIps`, `externalHosts`, `externalURLs`) are loaded and parsed
3. Matching is attempted in this order: Host → IP
4. First matching rule determines the proxy to use
5. If no rules match, the `defaultProxy` is used
6. Inverted rules (`not: true`) match everything EXCEPT the specified patterns

### External Rule Merging
When using `externalRule`, fields are merged as follows:
- **Ips, Hosts**: Concatenated with newlines, then parsed together
- **ExternalIps, ExternalHosts**: Also concatenated and loaded
- **Name**: Uses main rule name if specified, otherwise external rule name
- **Not**: Uses main rule setting if specified, otherwise external rule setting

This allows you to create modular rule configurations and combine multiple rule sources.

## Logging

GoProxy provides comprehensive logging with the following features:
- Multiple log levels: DEBUG, INFO, WARN, ERROR
- File logging with rotation
- Console output (except Windows GUI)
- Configurable log file size and retention

### Log Format
```
[LEVEL] message with context
```

Example:
```
[INFO] Starting proxy server on :8080
[INFO] HTTPS CONNECT to example.com via proxy socks5 (rule: 'External Domains')
[INFO] Blocking request to malicious.com (rule: 'Blocked Domains', proxy: 'block')
[INFO] Direct request to internal.company.com (rule: 'Local Networks', proxy: 'direct')
[DEBUG] Resolved target host example.com to [93.184.216.34]
```

## Architecture

### Core Components

- **Main Application** ([`main.go`](main.go)): Orchestrates the proxy servers, system tray, and auto-reload functionality
- **Configuration Management** ([`config/`](config/)): YAML config parsing, external rule loading, and management
- **Proxy Handler** ([`handler/`](handler/)): HTTP/HTTPS request handling and routing logic
- **SOCKS5 Handler** ([`handler/socks.go`](handler/socks.go)): SOCKS5 protocol implementation
- **Caching System** ([`cache/`](cache/)): DNS, pattern, and CIDR caching for performance
- **Logging System** ([`logging/`](logging/)): Configurable logging infrastructure with file rotation
- **System Tray** ([`tray/`](tray/)): Platform-specific system tray integration (Windows/macOS)
- **Ticker Manager** ([`ticker/`](ticker/)): Automatic periodic configuration reload functionality

### Proxy Handler Features

- **HTTP/HTTPS Support**: Full HTTP proxy functionality with CONNECT method support
- **SOCKS5 Support**: SOCKS5 proxy connections with authentication and UDP association
- **Dual Server Mode**: Simultaneous HTTP/HTTPS and SOCKS5 server operation
- **Connection Pooling**: Efficient connection reuse with configurable timeouts
- **Authentication**: Support for proxy authentication (Basic Auth for HTTP, User/Pass for SOCKS5)
- **Blocking**: Configurable request blocking with proper HTTP error responses
- **Source IP Binding**: Bind connections to specific interfaces or IP addresses
- **Custom DNS**: Configurable DNS resolution with source IP binding support
- **UDP Support**: SOCKS5 UDP association with session management

## Development

### Dependencies

- [`github.com/elazarl/goproxy`](https://github.com/elazarl/goproxy): Core HTTP/HTTPS proxy functionality
- [`github.com/getlantern/systray`](https://github.com/getlantern/systray): System tray integration
- [`github.com/gobwas/glob`](https://github.com/gobwas/glob): Pattern matching
- [`github.com/hashicorp/golang-lru/v2`](https://github.com/hashicorp/golang-lru/v2): LRU caching
- [`github.com/txthinking/socks5`](https://github.com/txthinking/socks5): SOCKS5 server implementation
- [`gopkg.in/yaml.v3`](https://github.com/go-yaml/yaml): YAML configuration parsing
- [`gopkg.in/natefinch/lumberjack.v2`](https://github.com/natefinch/lumberjack): Log file rotation
- [`github.com/skratchdot/open-golang`](https://github.com/skratchdot/open-golang): Opening config directory
- [`golang.org/x/net`](https://pkg.go.dev/golang.org/x/net): Extended networking capabilities
- [`golang.org/x/sync`](https://pkg.go.dev/golang.org/x/sync): Synchronization primitives

### Building and Testing

```bash
# Build for current platform
go build -o goProxy

# Run with custom config
./goProxy -config ./config.yaml

# Show version information
./goProxy -version
```

## License

This project is licensed under the MIT License. See the [LICENSE](LICENSE) file for details.
