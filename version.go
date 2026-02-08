package main

import (
	"fmt"
	"runtime/debug"
)

var Version = "1.4.3"

var (
	Commit    = "unknown"
	BuildTime = "unknown"
)

func GetVersion() string {
	version := fmt.Sprintf("GoProxy v%s", Version)

	if Commit != "unknown" && len(Commit) >= 8 {
		version += fmt.Sprintf(" (commit: %s)", Commit[:8])
	} else if Commit != "unknown" {
		version += fmt.Sprintf(" (commit: %s)", Commit)
	}

	if BuildTime != "unknown" {
		version += fmt.Sprintf(" built at %s", BuildTime)
	}

	return version
}

func GetBuildInfo() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "Build information not available"
	}

	var result string
	result += fmt.Sprintf("Go version: %s\n", info.GoVersion)
	result += fmt.Sprintf("Path: %s\n", info.Path)
	result += fmt.Sprintf("Main version: %s\n", info.Main.Version)

	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			result += fmt.Sprintf("Commit: %s\n", setting.Value)
		case "vcs.time":
			result += fmt.Sprintf("Build time: %s\n", setting.Value)
		case "vcs.modified":
			if setting.Value == "true" {
				result += "Working tree was modified\n"
			}
		}
	}

	return result
}
