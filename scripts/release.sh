#!/usr/bin/env sh

set -e

source "$(dirname $0)/_variables.sh"

VERSION=$VERSION go run scripts/version.go up

source "$(dirname $0)/_variables.sh"

git add scripts/_variables.sh
git commit -m "v$VERSION"
git push

go run scripts/version.go tag
git push origin "v$VERSION"