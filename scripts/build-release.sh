#!/usr/bin/env bash
# Copyright (C) 2026 Lance Duvall
# Project: <https://github.com/lanz/tenable-documentation-mcp>
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Builds static release archives for every supported platform into dist/, plus SHA256SUMS.
# Usage: scripts/build-release.sh v1.2.3
set -euo pipefail

version="${1:?usage: $0 <version, e.g. v1.2.3>}"
cmd="tenable-docs-mcp"
targets=(
  linux/amd64 linux/arm64
  darwin/amd64 darwin/arm64
  windows/amd64 windows/arm64
)

rm -rf dist
mkdir -p dist
for target in "${targets[@]}"; do
  goos="${target%/*}"
  goarch="${target#*/}"
  name="${cmd}_${version#v}_${goos}_${goarch}"
  exe="$cmd"
  [[ "$goos" == windows ]] && exe="$cmd.exe"
  stage="dist/$name"
  mkdir -p "$stage"
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build -trimpath \
    -ldflags "-s -w -X github.com/lanz/tenable-documentation-mcp/internal/version.Version=$version" \
    -o "$stage/$exe" "./cmd/$cmd"
  cp LICENSE README.md "$stage/"
  if [[ "$goos" == windows ]]; then
    (cd dist && zip -qr "$name.zip" "$name")
  else
    tar -C dist -czf "dist/$name.tar.gz" "$name"
  fi
  rm -rf "$stage"
done

# Text-mode format ("<hash>  <file>"), which `sha256sum -c SHA256SUMS` checks on any platform.
(cd dist && sha256sum --text -- *.tar.gz *.zip > SHA256SUMS)
cat dist/SHA256SUMS
