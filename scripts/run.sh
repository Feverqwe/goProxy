#!/usr/bin/env sh

set -e

source "$(dirname $0)/_variables.sh"

sh ./scripts/build.sh
./$BINARY