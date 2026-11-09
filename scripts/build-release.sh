#!/usr/bin/env bash
# Copyright (C) 2026 Lance Duvall
# Project: <https://github.com/lanz/tenable-docs-mcp>
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Builds static release archives for every supported platform into dist/; a Claude
# Desktop extension (an MCP Bundle, .mcpb) holding a binary for each desktop platform;
# server.json for the MCP Registry; SHA256SUMS; and the Linux binaries for the
# container image, in dist/docker.
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
    -ldflags "-s -w -X github.com/lanz/tenable-docs-mcp/internal/version.Version=$version" \
    -o "$stage/$exe" "./cmd/$cmd"
  cp LICENSE README.md "$stage/"
  if [[ "$goos" == windows ]]; then
    (cd dist && zip -qr "$name.zip" "$name")
  else
    tar -C dist -czf "dist/$name.tar.gz" "$name"
  fi
  # Keep the binaries the extension needs.
  mkdir -p "dist/bin/${goos}_${goarch}"
  cp "$stage/$exe" "dist/bin/${goos}_${goarch}/"
  rm -rf "$stage"
done

# The Claude Desktop extension: a universal macOS binary, and the amd64 binaries for
# Windows (which Windows on Arm runs too) and Linux, with a manifest that picks one.
bundle="dist/mcpb"
mkdir -p "$bundle/server/darwin" "$bundle/server/win32" "$bundle/server/linux"
go run ./scripts/fatmacho "$bundle/server/darwin/$cmd" "dist/bin/darwin_amd64/$cmd" "dist/bin/darwin_arm64/$cmd"
cp "dist/bin/windows_amd64/$cmd.exe" "$bundle/server/win32/"
cp "dist/bin/linux_amd64/$cmd" "$bundle/server/linux/"
chmod +x "$bundle/server/darwin/$cmd" "$bundle/server/linux/$cmd"
go run ./scripts/mcpb -version "$version" -out "$bundle/manifest.json"
cp LICENSE README.md "$bundle/"
mcpb_file="${cmd}_${version#v}.mcpb"
(cd "$bundle" && zip -qrX "../$mcpb_file" .)
# The Linux binaries, for the container image (see Dockerfile).
for arch in amd64 arm64; do
  mkdir -p "dist/docker/linux_$arch"
  cp "dist/bin/linux_$arch/$cmd" "dist/docker/linux_$arch/"
done
mkdir -p dist/docker/data
rm -rf "$bundle" dist/bin

# server.json for the MCP Registry, with this version and the extension's checksum.
mcpb_sha=$(sha256sum "dist/$mcpb_file" | cut -d' ' -f1)
sed -e "s/0\.0\.0-dev/${version#v}/g" -e "s/MCPB_SHA256/$mcpb_sha/" -e "s/MCPB_FILE/$mcpb_file/" -e "s/RELEASE_TAG/$version/" \
  server.json > dist/server.json

# Text-mode format ("<hash>  <file>"), which `sha256sum -c SHA256SUMS` checks on any platform.
(cd dist && sha256sum --text -- *.tar.gz *.zip *.mcpb > SHA256SUMS)
cat dist/SHA256SUMS
