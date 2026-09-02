#!/usr/bin/env sh

set -eu

project_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
output=${OUTPUT:-"$project_root/bin/hs"}

mkdir -p "$(dirname -- "$output")"
cd "$project_root"

go build -trimpath -o "$output" ./cmd/hs
assets="$project_root/internal/app/web_static"
asset_output="$(dirname -- "$output")/web_static"
mkdir -p "$asset_output"
cp -R "$assets"/. "$asset_output"/
printf 'Built %s\n' "$output"
printf 'Copied web assets to %s\n' "$asset_output"
