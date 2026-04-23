package config

import (
	"crypto/sha256"
	"fmt"
	"goProxy/internal/logging"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

func downloadAndCacheFile(downloadURL string, cacheOnly bool, httpClientFunc HTTPClientFunc, logger *logging.Logger, forceReload bool, ttl int) (string, error) {
	cacheFile := getCacheFilePath(downloadURL)

	cacheAvailable := false
	if _, err := os.Stat(cacheFile); err == nil {
		cacheAvailable = true
	}

	if cacheOnly {
		if cacheAvailable {
			return cacheFile, nil
		}
		return "", fmt.Errorf("cached file not found for %s", downloadURL)
	}

	if cacheAvailable {
		if shouldDownload, err := shouldDownloadFile(cacheFile, forceReload, ttl); err != nil {
			logger.Warn("Failed to check cache TTL for %s: %v", downloadURL, err)
		} else if !shouldDownload {
			return cacheFile, nil
		}
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

	return downloadWithClient(downloadURL, cacheFile, client, logger)
}

func downloadWithClient(downloadURL, cacheFile string, client *http.Client, logger *logging.Logger) (string, error) {
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

	tempFile := cacheFile + ".tmp"
	file, err := os.Create(tempFile)
	if err != nil {
		return "", fmt.Errorf("failed to create temporary file: %v", err)
	}
	defer file.Close()

	_, err = io.Copy(file, resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to write temporary file: %v", err)
	}

	file.Close()

	if _, err := os.Stat(cacheFile); err == nil {
		os.Remove(cacheFile)
	}

	err = os.Rename(tempFile, cacheFile)
	if err != nil {
		return "", fmt.Errorf("failed to rename temporary file to cache file: %v", err)
	}

	err = saveDownloadTimestamp(cacheFile)
	if err != nil {
		logger.Warn("Failed to save download timestamp for %s: %v", downloadURL, err)
	}

	return cacheFile, nil
}

func shouldDownloadFile(cacheFile string, forceReload bool, ttl int) (bool, error) {
	if forceReload {
		return true, nil
	}

	updatedAtFile := cacheFile + ".updated_at"
	if _, err := os.Stat(cacheFile); err != nil {
		return true, nil
	}

	content, err := os.ReadFile(updatedAtFile)
	if err != nil {
		return true, fmt.Errorf("failed to read timestamp file: %v", err)
	}

	timestampInt, err := strconv.ParseInt(strings.TrimSpace(string(content)), 10, 64)
	if err != nil {
		return true, fmt.Errorf("failed to parse Unix timestamp: %v", err)
	}

	expiresAt := timestampInt + int64(ttl)
	currentTime := time.Now().Unix()
	if currentTime > expiresAt {
		return true, nil
	}

	return false, nil
}

func saveDownloadTimestamp(cacheFile string) error {
	updatedAtFile := cacheFile + ".updated_at"
	timestamp := time.Now().Unix()
	return os.WriteFile(updatedAtFile, fmt.Appendf(nil, "%d", timestamp), 0644)
}
