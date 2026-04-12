package tray

import (
	"fmt"
	"goProxy/internal"
	"goProxy/internal/logging"

	"github.com/skratchdot/open-golang/open"
	"github.com/sqweek/dialog"
)

func CheckForUpdates(version string, latestReleaseAPI string, releasesURL string, logger *logging.Logger) {
	latestVersion, err := internal.GetLatestVersionFromGitHub(latestReleaseAPI)
	if err != nil {
		logger.Error("Failed to check for updates: %v", err)
		dialog.Message("%s", fmt.Sprintf("Failed to check for updates: %v", err)).Title("Update Check Failed").Error()
		return
	}

	isNewer, err := internal.CompareVersions(version, latestVersion)
	if err != nil {
		logger.Error("Failed to compare versions: %v", err)
		dialog.Message("%s", fmt.Sprintf("Failed to compare versions: %v", err)).Title("Update Check Failed").Error()
		return
	}

	if isNewer {
		message := fmt.Sprintf("A new version (v%s) is available! Would you like to download it now?", latestVersion)
		dialog.Message("%s", message).Title("New Version Available").Info()
		if err := open.Run(releasesURL); err != nil {
			logger.Error("Failed to open releases page: %v", err)
		}
	} else {
		message := fmt.Sprintf("You're using the latest version (v%s). No updates available.", version)
		dialog.Message("%s", message).Title("Up to Date").Info()
	}
}
