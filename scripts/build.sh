#!/usr/bin/env sh

set -eu

project_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
output=${OUTPUT:-"$project_root/bin/hs"}

mkdir -p "$(dirname -- "$output")"
cd "$project_root"

go build -trimpath -o "$output" ./cmd/hs
printf 'Built %s\n' "$output"
