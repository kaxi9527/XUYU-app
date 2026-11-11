#!/bin/sh
# Build only the owned model runtime. Uses the existing versioned Go cache.
set -eu
desktop_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
output_path=${1:-"$desktop_dir/bin/workshelf-model-core"}
case "$output_path" in /*) ;; *) output_path="$desktop_dir/$output_path";; esac
mkdir -p "$(dirname -- "$output_path")"
cd "$desktop_dir/model-core"
GOMAXPROCS=2 GOPROXY=off GOTOOLCHAIN=local "${WORKSHELF_GO:-go}" build -mod=readonly -p 1 -trimpath -o "$output_path" ./cmd/workshelf-core
