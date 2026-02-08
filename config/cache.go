package config

import (
	"crypto/sha256"
	"fmt"
	"goProxy/logger"
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

func downloadAndCacheFile(downloadURL string, cacheOnly bool, httpClientFunc HTTPClientNoConfigFunc) (string, error) {
	cacheFile := getCacheFilePath(downloadURL)

	if cacheOnly {
		if _, err := os.Stat(cacheFile); err == nil {
			return cacheFile, nil
		}
		return "", fmt.Errorf("cached file not found for %s", downloadURL)
	}

	var client *http.Client
	if httpClientFunc != nil {
		var err error
		client, err = httpClientFunc(downloadURL)
		if err != nil {
			if _, cacheErr := os.Stat(cacheFile); cacheErr == nil {
				logger.Warn("Failed to create HTTP client for %s: %v, using cached file", downloadURL, err)
				return cacheFile, nil
			}
			return "", fmt.Errorf("failed to create HTTP client for %s: %v", downloadURL, err)
		}
	} else {
		client = &http.Client{
			Timeout: 30 * time.Second,
		}
	}

	return downloadWithClient(downloadURL, cacheFile, client)
}

func downloadWithClient(downloadURL, cacheFile string, client *http.Client) (string, error) {
	resp, err := client.Get(downloadURL)
	if err != nil {
		if _, cacheErr := os.Stat(cacheFile); cacheErr == nil {
			logger.Warn("Failed to download %s: %v, using cached file", downloadURL, err)
			return cacheFile, nil
		}
		return "", fmt.Errorf("failed to download %s: %v", downloadURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if _, cacheErr := os.Stat(cacheFile); cacheErr == nil {
			logger.Warn("Failed to download %s: status %d, using cached file", downloadURL, resp.StatusCode)
			return cacheFile, nil
		}
		return "", fmt.Errorf("failed to download %s: status %d", downloadURL, resp.StatusCode)
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
