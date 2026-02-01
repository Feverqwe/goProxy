package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// Version represents a semantic version
type Version struct {
	Major      int
	Minor      int
	Patch      int
	Prerelease string
	Build      string
}

// ParseVersion parses a semantic version string
func ParseVersion(version string) (*Version, error) {
	// Regex for semantic version: MAJOR.MINOR.PATCH[-PRERELEASE][+BUILD]
	re := regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)(?:-([a-zA-Z0-9\.]+))?(?:\+([a-zA-Z0-9\.]+))?$`)
	matches := re.FindStringSubmatch(version)

	if matches == nil {
		return nil, fmt.Errorf("invalid version format: %s", version)
	}

	v := &Version{}
	v.Major, _ = strconv.Atoi(matches[1])
	v.Minor, _ = strconv.Atoi(matches[2])
	v.Patch, _ = strconv.Atoi(matches[3])

	if len(matches) > 4 {
		v.Prerelease = matches[4]
	}
	if len(matches) > 5 {
		v.Build = matches[5]
	}

	return v, nil
}

// String returns the version as a string
func (v *Version) String() string {
	version := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)

	if v.Prerelease != "" {
		version += "-" + v.Prerelease
	}
	if v.Build != "" {
		version += "+" + v.Build
	}

	return version
}

// Increment increments the version based on the type
func (v *Version) Increment(incrementType string) error {
	switch incrementType {
	case "patch":
		v.Patch++
	case "minor":
		v.Minor++
		v.Patch = 0
	case "major":
		v.Major++
		v.Minor = 0
		v.Patch = 0
	default:
		return fmt.Errorf("invalid increment type: %s (valid: patch, minor, major)", incrementType)
	}

	// Clear prerelease and build when incrementing
	v.Prerelease = ""
	v.Build = ""

	return nil
}

// File paths
const (
	versionFile   = "version.go"
	variablesFile = "scripts/_variables.sh"
)

// GetCurrentVersion reads the current version from version.go
func GetCurrentVersion() (string, error) {
	file, err := os.Open(versionFile)
	if err != nil {
		return "", fmt.Errorf("failed to open version file: %v", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "var Version = ") {
			// Extract version from line like: var Version = "1.2.3"
			re := regexp.MustCompile(`var Version = "([^"]+)"`)
			matches := re.FindStringSubmatch(line)
			if matches != nil && len(matches) > 1 {
				return matches[1], nil
			}
		}
	}

	return "", fmt.Errorf("version not found in %s", versionFile)
}

// UpdateVersionFile updates the version in version.go
func UpdateVersionFile(newVersion string) error {
	content, err := os.ReadFile(versionFile)
	if err != nil {
		return fmt.Errorf("failed to read version file: %v", err)
	}

	// Replace version in the file
	re := regexp.MustCompile(`var Version = "[^"]+"`)
	newContent := re.ReplaceAllString(string(content), fmt.Sprintf(`var Version = "%s"`, newVersion))

	err = os.WriteFile(versionFile, []byte(newContent), 0644)
	if err != nil {
		return fmt.Errorf("failed to write version file: %v", err)
	}

	fmt.Printf("✓ Updated %s with version: %s\n", versionFile, newVersion)
	return nil
}

// UpdateVariablesFile updates the version in _variables.sh
func UpdateVariablesFile(newVersion string) error {
	content, err := os.ReadFile(variablesFile)
	if err != nil {
		// It's okay if variables file doesn't exist
		fmt.Printf("⚠ Variables file not found: %s\n", variablesFile)
		return nil
	}

	// Replace version in the file
	re := regexp.MustCompile(`VERSION=[^\n]*`)
	newContent := re.ReplaceAllString(string(content), fmt.Sprintf("VERSION=%s", newVersion))

	err = os.WriteFile(variablesFile, []byte(newContent), 0644)
	if err != nil {
		return fmt.Errorf("failed to write variables file: %v", err)
	}

	fmt.Printf("✓ Updated %s with version: %s\n", variablesFile, newVersion)
	return nil
}

// CreateGitTag creates a git tag for the current version
func CreateGitTag(version string) error {
	tagName := "v" + version

	// Check if we're in a git repository
	if _, err := os.Stat("./.git"); os.IsNotExist(err) {
		return fmt.Errorf("not in a git repository")
	}

	// Check if tag already exists
	cmd := fmt.Sprintf("git rev-parse %s > /dev/null 2>&1", tagName)
	if system(cmd) == nil {
		return fmt.Errorf("tag %s already exists", tagName)
	}

	// Create the tag
	cmd = fmt.Sprintf("git tag -a %s -m \"Release %s\"", tagName, tagName)
	if err := system(cmd); err != nil {
		return fmt.Errorf("failed to create git tag: %v", err)
	}

	fmt.Printf("✓ Created git tag: %s\n", tagName)
	fmt.Printf("To push the tag, run: git push origin %s\n", tagName)
	return nil
}

// system runs a shell command and returns error if it fails
func system(cmd string) error {
	return exec.Command("sh", "-c", cmd).Run()
}

// ShowHelp displays usage information
func ShowHelp() {
	fmt.Println(`Version management script for GoProxy

Usage: go run scripts/version.go [command] [options]

Commands:
  get                    - Get current version
  set <version>          - Set version to specific value
  inc [patch|minor|major] - Increment version (default: patch)
  tag                    - Create git tag for current version
  help                   - Show this help

Examples:
  go run scripts/version.go get                    # Get current version
  go run scripts/version.go set 1.2.3             # Set version to 1.2.3
  go run scripts/version.go inc patch             # Increment patch version (1.2.3 -> 1.2.4)
  go run scripts/version.go inc minor             # Increment minor version (1.2.3 -> 1.3.0)
  go run scripts/version.go inc major             # Increment major version (1.2.3 -> 2.0.0)
  go run scripts/version.go tag                   # Create git tag for current version

Version format: MAJOR.MINOR.PATCH[-PRERELEASE][+BUILD]`)
}

func main() {
	if len(os.Args) < 2 {
		ShowHelp()
		os.Exit(1)
	}

	command := os.Args[1]

	switch command {
	case "get":
		version, err := GetCurrentVersion()
		if err != nil {
			fmt.Printf("❌ Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Current version: %s\n", version)

	case "set":
		if len(os.Args) < 3 {
			fmt.Println("❌ Error: version argument required for 'set' command")
			os.Exit(1)
		}

		newVersion := os.Args[2]
		_, err := ParseVersion(newVersion)
		if err != nil {
			fmt.Printf("❌ Error: %v\n", err)
			os.Exit(1)
		}

		currentVersion, err := GetCurrentVersion()
		if err != nil {
			fmt.Printf("❌ Error: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Setting version: %s -> %s\n", currentVersion, newVersion)

		if err := UpdateVersionFile(newVersion); err != nil {
			fmt.Printf("❌ Error: %v\n", err)
			os.Exit(1)
		}

		if err := UpdateVariablesFile(newVersion); err != nil {
			fmt.Printf("❌ Error: %v\n", err)
			os.Exit(1)
		}

	case "inc":
		incrementType := "patch"
		if len(os.Args) > 2 {
			incrementType = os.Args[2]
		}

		currentVersionStr, err := GetCurrentVersion()
		if err != nil {
			fmt.Printf("❌ Error: %v\n", err)
			os.Exit(1)
		}

		currentVersion, err := ParseVersion(currentVersionStr)
		if err != nil {
			fmt.Printf("❌ Error: %v\n", err)
			os.Exit(1)
		}

		if err := currentVersion.Increment(incrementType); err != nil {
			fmt.Printf("❌ Error: %v\n", err)
			os.Exit(1)
		}

		newVersion := currentVersion.String()
		fmt.Printf("Incrementing %s version: %s -> %s\n", incrementType, currentVersionStr, newVersion)

		if err := UpdateVersionFile(newVersion); err != nil {
			fmt.Printf("❌ Error: %v\n", err)
			os.Exit(1)
		}

		if err := UpdateVariablesFile(newVersion); err != nil {
			fmt.Printf("❌ Error: %v\n", err)
			os.Exit(1)
		}

	case "tag":
		currentVersion, err := GetCurrentVersion()
		if err != nil {
			fmt.Printf("❌ Error: %v\n", err)
			os.Exit(1)
		}

		if err := CreateGitTag(currentVersion); err != nil {
			fmt.Printf("❌ Error: %v\n", err)
			os.Exit(1)
		}

	case "help", "--help", "-h":
		ShowHelp()

	default:
		fmt.Printf("❌ Error: unknown command: %s\n", command)
		ShowHelp()
		os.Exit(1)
	}
}
