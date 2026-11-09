# Copyright (C) 2026 Lance Duvall
# Project: <https://github.com/lanz/tenable-docs-mcp>
# SPDX-License-Identifier: GPL-3.0-or-later
#
# The container image: the release's static Linux binary in a minimal base that runs as
# a user without privileges. Build the binaries first with scripts/build-release.sh,
# which leaves them in dist/docker; then, for example:
#
#   docker buildx build --platform linux/amd64,linux/arm64 -t tenable-docs-mcp .
#
# Run it over stdio, as MCP clients do, with a volume so the index is kept between runs:
#
#   docker run -i --rm --mount type=volume,src=tenable-docs-mcp,dst=/data ghcr.io/lanz/tenable-docs-mcp

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab

ARG TARGETARCH

LABEL io.modelcontextprotocol.server.name="io.github.lanz/tenable-docs-mcp" \
      org.opencontainers.image.title="Tenable Docs MCP" \
      org.opencontainers.image.description="Cited answers from Tenable's product documentation, in every product, version, and language" \
      org.opencontainers.image.source="https://github.com/lanz/tenable-docs-mcp" \
      org.opencontainers.image.licenses="GPL-3.0-or-later"

COPY dist/docker/linux_${TARGETARCH}/tenable-docs-mcp /usr/local/bin/tenable-docs-mcp
# The volume's folder, owned by the user the server runs as, so a new volume is
# writable: the index in /data/index, and recorded questions and the feedback log in
# /data/config.
COPY --chown=nonroot:nonroot dist/docker/data /data

ENV TDM_INDEX_DIR=/data/index \
    XDG_CONFIG_HOME=/data/config
VOLUME ["/data"]

ENTRYPOINT ["/usr/local/bin/tenable-docs-mcp"]
CMD ["serve"]
