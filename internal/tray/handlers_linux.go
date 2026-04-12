//go:build linux

package tray

import (
	"goProxy/internal/logging"
)

func CheckForUpdates(version string, latestReleaseAPI string, releasesURL string, logger *logging.Logger) {
	// Not implemented on Linux
}
