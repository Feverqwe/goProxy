package tray

import (
	"goProxy/internal"
	"goProxy/logging"

	"github.com/skratchdot/open-golang/open"
)

func CheckForUpdates(version string, latestReleaseAPI string, releasesURL string, logger *logging.Logger) {
	logger.Info("Checking for updates...")
	latestVersion, err := internal.GetLatestVersionFromGitHub(latestReleaseAPI)
	if err != nil {
		logger.Error("Failed to check for updates: %v", err)
		return
	}

	isNewer, err := internal.CompareVersions(version, latestVersion)
	if err != nil {
		logger.Error("Failed to compare versions: %v", err)
		return
	}

	if isNewer {
		logger.Info("New version available: v%s", latestVersion)
		if err := open.Run(releasesURL); err != nil {
			logger.Error("Failed to open releases page: %v", err)
		}
	} else {
		logger.Info("You are using the latest version (v%s)", version)
	}
}
