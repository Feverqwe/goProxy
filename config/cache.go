package config

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

func getCacheDir() string {
	profileDir := getProfilePath()
	cacheDir := filepath.Join(profileDir, "cache")

	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return filepath.Join(profileDir, "cache")
	}

	return cacheDir
}

func getCacheFilePath(url string) string {
	baseName := filepath.Base(url)
	if baseName == "" || baseName == "." || baseName == "/" {
		baseName = "rules"
	}

	hash := sha256.Sum256([]byte(url))
	filename := fmt.Sprintf("%s_%x.txt", baseName, hash[:8])

	return filepath.Join(getCacheDir(), filename)
}

func downloadAndCacheFile(url string) (string, error) {
	return downloadAndCacheFileWithMode(url, false)
}

func downloadAndCacheFileWithMode(url string, cacheOnly bool) (string, error) {
	cacheFile := getCacheFilePath(url)

	if cacheOnly {
		if _, err := os.Stat(cacheFile); err == nil {
			return cacheFile, nil
		}
		return "", fmt.Errorf("cached file not found for %s", url)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		if _, cacheErr := os.Stat(cacheFile); cacheErr == nil {
			return cacheFile, nil
		}
		return "", fmt.Errorf("failed to download %s: %v", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if _, cacheErr := os.Stat(cacheFile); cacheErr == nil {
			return cacheFile, nil
		}
		return "", fmt.Errorf("failed to download %s: status %d", url, resp.StatusCode)
	}

	file, err := os.Create(cacheFile)
	if err != nil {
		return "", fmt.Errorf("failed to create cache file: %v", err)
	}
	defer file.Close()

	_, err = io.Copy(file, resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to write cache file: %v", err)
	}

	return cacheFile, nil
}
