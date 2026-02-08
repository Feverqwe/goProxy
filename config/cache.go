package config

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

func downloadAndCacheFile(downloadURL string, config *ProxyConfig) (string, error) {
	return downloadAndCacheFileWithMode(downloadURL, false, config)
}

func downloadAndCacheFileWithMode(downloadURL string, cacheOnly bool, config *ProxyConfig) (string, error) {
	cacheFile := getCacheFilePath(downloadURL)

	if cacheOnly {
		if _, err := os.Stat(cacheFile); err == nil {
			return cacheFile, nil
		}
		return "", fmt.Errorf("cached file not found for %s", downloadURL)
	}

	proxyURLStr := config.GetProxyServerURL()
	proxyURL, err := url.Parse(proxyURLStr)
	if err != nil {
		return "", fmt.Errorf("failed to parse proxy URL: %v", err)
	}

	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
		},
	}

	resp, err := client.Get(downloadURL)
	if err != nil {
		if _, cacheErr := os.Stat(cacheFile); cacheErr == nil {
			return cacheFile, nil
		}
		return "", fmt.Errorf("failed to download %s via proxy: %v", downloadURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if _, cacheErr := os.Stat(cacheFile); cacheErr == nil {
			return cacheFile, nil
		}
		return "", fmt.Errorf("failed to download %s via proxy: status %d", downloadURL, resp.StatusCode)
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
