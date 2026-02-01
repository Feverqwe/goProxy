package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const APP_ID = "com.rndnm.goproxy"

func getProfilePath() string {
	place := ""
	for _, e := range os.Environ() {
		pair := strings.SplitN(e, "=", 2)
		if pair[0] == "PROFILE_PLACE" {
			place = pair[1]
		}
	}
	if place == "" {
		place = getDefaultProfilePath()
	}
	return place
}

func getDefaultProfilePath() string {
	place := ""
	if runtime.GOOS == "windows" {
		pwd, err := os.Getwd()
		if err != nil {
			panic(err)
		}
		place = pwd
	} else if runtime.GOOS == "darwin" {
		place = os.Getenv("HOME") + "/Library/Application Support/" + APP_ID
	} else {
		ex, err := os.Executable()
		if err != nil {
			panic(err)
		}
		place = filepath.Dir(ex)
	}
	return place
}

func GetConfigPath() string {
	profileDir := getProfilePath()

	if err := os.MkdirAll(profileDir, 0700); err != nil {
		panic(err)
	}

	return filepath.Join(profileDir, "config.yaml")
}
