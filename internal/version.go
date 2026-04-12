package internal

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/hashicorp/go-version"
)

type GitHubRelease struct {
	TagName string `json:"tag_name"`
}

func GetLatestVersionFromGitHub(url string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub API request failed with status: %d", resp.StatusCode)
	}

	var release GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", err
	}

	version := strings.TrimPrefix(release.TagName, "v")

	return version, nil
}

func CompareVersions(v1, v2 string) (bool, error) {
	version1, err := version.NewVersion(v1)
	if err != nil {
		return false, err
	}

	version2, err := version.NewVersion(v2)
	if err != nil {
		return false, err
	}

	return version1.LessThan(version2), nil
}
