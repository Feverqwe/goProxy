# GoProxy - Advanced HTTP/HTTPS Proxy Server

GoProxy is a sophisticated HTTP/HTTPS proxy server written in Go that provides intelligent routing, caching, and system tray integration. It supports multiple proxy types, rule-based routing, and hot-reloadable configuration.

## Features

- **Multi-Protocol Support**: HTTP, HTTPS, SOCKS5 proxy support
- **Rule-Based Routing**: Advanced pattern matching for IPs, hosts, and URLs
- **Hot Reload**: Configuration reload without restart (SIGHUP or tray menu)
- **System Tray Integration**: Native system tray support (Windows/macOS)
- **Caching**: DNS and pattern caching for performance
- **Logging**: Configurable logging with file rotation
- **Cross-Platform**: Windows, macOS, and Linux support
- **Blocking Support**: Ability to block specific domains/URLs

## Installation

### Pre-built Binaries

Download the latest release from the [Releases page](https://github.com/rndnm/goProxy/releases) for your platform:

- **Windows**: `goProxy.exe` (console application with system tray)
- **macOS**: `GoProxy.app` (native macOS application bundle)
- **Linux**: `goProxy` (command-line binary)

### Building from Source

1. **Prerequisites**: Go 1.24.0 or later
2. **Clone the repository**:
   ```bash
   git clone https://github.com/rndnm/goProxy.git
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

listenAddr: ":8080"
logLevel: "info"
logFile: "goProxy.log"
maxLogSize: 10
maxLogFiles: 5

rules:
  # Rule 1: Direct connection for local networks and internal domains
  - name: "Local Networks"
    proxy: "direct"
    ips: "192.168.1.0/24 10.0.0.0/8 172.16.0.0/12"
    hosts: "localhost*, *.local, *.example.com, internal.company.com"
    urls: "http://internal-api.company.com/v1/* https://*.internal.com/api/*"

  # Rule 2: Use SOCKS5 proxy for specific domains (inverted rule)
  - name: "Inverted Proxy Rule"
    proxy: "socks5"
    not: true
    hosts: "*.google.com, *.youtube.com, *.facebook.com"

  # Rule 3: Use HTTP proxy for other external domains
  - name: "External Domains"
    proxy: "http"
    hosts: "*.external.com api.*.com"

  # Rule 4: Block specific domains with external rule lists
  - name: "Blocked Domains"
    proxy: "block"
    externalHosts: "https://raw.githubusercontent.com/example/malicious-domains/main/blocklist.txt"
    externalURLs: "./local-blocklist.txt"
```

### Configuration Options

#### Global Settings
- `defaultProxy`: Default proxy to use when no rules match
- `proxies`: Map of proxy definitions
- `listenAddr`: Address and port to listen on (e.g., ":8080")
- `logLevel`: Logging level (debug, info, warn, error, none)
- `logFile`: Log file path (relative to config directory)
- `maxLogSize`: Maximum log file size in MB before rotation
- `maxLogFiles`: Number of backup log files to keep

#### Proxy Definitions
- `direct`: No proxy (direct connection)
- `block`: Block the connection entirely
- Custom proxies: HTTP/HTTPS/SOCKS5 URLs

#### Rule Configuration
Rules are evaluated in order. Each rule can match based on:
- `name`: Optional descriptive name for the rule (used in logging)
- `ips`: CIDR notation or IP addresses
- `hosts`: Hostname patterns with wildcards (`*.example.com`)
- `urls`: Full URL patterns with wildcards
- `externalIps`: External sources for IP rules (URLs or local file paths)
- `externalHosts`: External sources for host rules (URLs or local file paths)
- `externalURLs`: External sources for URL rules (URLs or local file paths)
- `not`: Invert the rule logic (match everything EXCEPT the patterns)

#### External Rule Sources
GoProxy supports loading rules from external sources:
- **URLs**: HTTP/HTTPS endpoints (automatically cached)
- **Local files**: Relative to config directory or absolute paths
- **Caching**: External rules are cached locally for performance
- **Fallback**: Uses cached version if external source is unavailable

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

GoProxy uses sophisticated pattern matching:

### Host Matching
- Supports wildcards: `*.example.com` matches `sub.example.com` but not `example.com`
- Automatically expands wildcards: `*.example.com` also matches `example.com`
- Handles ports: `example.com:8080` is properly parsed

### IP Matching
- CIDR notation: `192.168.1.0/24`
- Individual IPs: `192.168.1.1`
- Domain resolution: Hostnames are resolved to IPs for matching

### URL Matching
- Full URL patterns: `https://api.example.com/v1/*`
- Supports wildcards in any part of the URL

### Rule Evaluation
1. Rules are processed in order from top to bottom
2. External rules are loaded and merged with local rules
3. First matching rule determines the proxy to use
4. If no rules match, the `defaultProxy` is used
5. Inverted rules (`not: true`) match everything EXCEPT the specified patterns

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

- **Main Application** ([`main.go`](main.go)): Orchestrates the proxy server and system tray
- **Configuration Management** ([`config/`](config/)): YAML config parsing and management with external rule loading
- **Proxy Handler** ([`handler/`](handler/)): HTTP request handling and routing logic
- **Caching System** ([`cache/`](cache/)): DNS, pattern, and external rule caching for performance
- **Logging System** ([`logging/`](logging/)): Configurable logging infrastructure
- **System Tray** ([`tray/`](tray/)): Platform-specific system tray integration

### Key Design Patterns

- **Singleton Config Manager**: Thread-safe configuration access
- **Strategy Pattern**: Different proxy implementations (direct, HTTP, SOCKS5, block)
- **Observer Pattern**: Hot-reload configuration changes
- **Factory Pattern**: Proxy handler creation based on configuration

## Development

### Dependencies

- [`github.com/elazarl/goproxy`](https://github.com/elazarl/goproxy): Core proxy functionality
- [`github.com/getlantern/systray`](https://github.com/getlantern/systray): System tray integration
- [`github.com/gobwas/glob`](https://github.com/gobwas/glob): Pattern matching
- [`gopkg.in/natefinch/lumberjack.v2`](https://github.com/natefinch/lumberjack): Log rotation
- [`gopkg.in/yaml.v3`](https://github.com/go-yaml/yaml): YAML configuration parsing

### Building and Testing

```bash
# Build for current platform
go build -o goProxy
```

## License

This project is licensed under the MIT License. See the [LICENSE](LICENSE) file for details.
