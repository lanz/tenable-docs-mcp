# Tenable Docs MCP

An MCP server that gives AI assistants cited answers from Tenable's product documentation:
how to do a task, what a setting does, which role it needs, what a system requires, and
what changed in a release. It covers [docs.tenable.com](https://docs.tenable.com) and its
translated sites, in every product, version, and language, and links each answer to the
exact section.

For questions about Tenable's APIs, use the
[Tenable Developer Portal MCP](https://developer.tenable.com/mcp), which
developer.tenable.com hosts. The two work together.

> **Status: in development.** This version serves MCP over stdio and HTTP, and has one tool,
> `index_status`. Search, reading, and the other tools are on the way.

## Build

Go 1.26 or later:

```bash
go build ./cmd/tenable-docs-mcp
./tenable-docs-mcp version
```

## Run

```bash
tenable-docs-mcp                             # MCP over stdio, for MCP clients
tenable-docs-mcp serve -http 127.0.0.1:8080  # MCP over HTTP at http://127.0.0.1:8080/mcp
```

The HTTP server listens only on this machine, and refuses cross-origin browser requests and
requests under other host names.

## Privacy

The server sends no telemetry. When the documentation tools arrive, it will connect only to
Tenable's documentation sites. This section will list every connection it makes.

## License

[GPL-3.0-or-later](LICENSE). Copyright (C) 2026 Lance Duvall.

## Disclaimer

This is a community project. It isn't an official Tenable product, and Tenable doesn't
provide support for it. Tenable, Tenable One, Nessus, and other Tenable product names are
trademarks of Tenable, Inc.
