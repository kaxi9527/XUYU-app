#!/bin/sh
set -eu
desktop_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
output_path=${1:-"$desktop_dir/bin/workshelf"}
case "$output_path" in /*) ;; *) output_path="$desktop_dir/$output_path";; esac
mkdir -p "$(dirname -- "$output_path")"
cd "$desktop_dir/.."
GOMAXPROCS=2 GOPROXY=off GOTOOLCHAIN=local "${WORKSHELF_GO:-go}" build -mod=readonly -p 1 -trimpath -o "$output_path" ./cmd/workshelf
