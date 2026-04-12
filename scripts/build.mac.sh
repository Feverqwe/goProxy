#!/usr/bin/env sh

set -e

source "$(dirname $0)/_variables.sh"

sh ./scripts/build.sh ${BINARY}

appify=./scripts/simple_appify.sh

if [ -e "./${NAME}.app" ]; then
    rm -r "./${NAME}.app" | true
fi

$appify -menubar -allowinsecureconnections -name "${NAME}" -author "${AUTHOR}" -id "${APP_ID}" -icon "${ICON_PATH}" -version "${VERSION}" "${BINARY}"
