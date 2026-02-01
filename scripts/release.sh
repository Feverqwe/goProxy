#!/usr/bin/env sh

set -e

go run scripts/version.go inc patch

source "$(dirname $0)/_variables.sh"

git add version.go

git commit -m "v$VERSION"

go run scripts/version.go tag

git push origin "v$VERSION"