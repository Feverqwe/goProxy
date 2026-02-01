#!/usr/bin/env sh

# Build variables for GoProxy macOS app
NAME="GoProxy"
AUTHOR="RNDNM"
APP_ID="com.rndnm.goproxy"
BINARY="goProxy"
ICON_PATH="./assets/icon.icns"

# Version variables (can be overridden by environment)
VERSION=1.0.6
COMMIT=${COMMIT:-unknown}
BUILD_TIME=${BUILD_TIME:-unknown}