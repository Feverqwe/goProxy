#!/usr/bin/env sh

set -e

source "$(dirname $0)/_variables.sh"

if [ -f "./${BINARY}" ]; then
    rm ./${BINARY}
fi

if [ "$(uname)" = "Darwin" ]; then
    export CGO_LDFLAGS="-Wl,-no_warn_duplicate_libraries"
fi

go build -trimpath -ldflags "-X main.Version=$VERSION" -o ${BINARY}