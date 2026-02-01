#!/usr/bin/env sh

set -e

source "$(dirname $0)/_variables.sh"

if [ -f "./${BINARY}" ]; then
    rm ./${BINARY}
fi

go build -trimpath -ldflags "-X main.Version=${VERSION:-dev} -X main.Commit=${COMMIT:-unknown} -X main.BuildTime=${BUILD_TIME:-unknown}" -o ${BINARY}