# Connecting MCP clients

How to add the Tenable Docs MCP to each MCP client, together with the [Tenable Developer Portal MCP](https://developer.tenable.com/mcp), which answers questions about Tenable's APIs. The two work as a pair: this server answers from the product docs, the Developer Portal's from the API reference, and the [companion skill](../README.md#the-companion-skill) tells the assistant which to use.

First, [install the server](../README.md#install). Then:

```bash
tenable-docs-mcp config
```

prints every client's settings below with this computer's path to the binary already filled in and quoted correctly; `-client NAME` prints one (`claude-code`, `claude-desktop`, `vscode`, `cursor`, `devin-desktop`, `gemini`, or `codex`). It only prints, and never edits a client's files.

In the examples, `/path/to/tenable-docs-mcp` is the binary's full path. On Windows, JSON needs doubled backslashes (`"C:\\tools\\tenable-docs-mcp.exe"`), and shell commands take forward slashes.

- [Claude Desktop](#claude-desktop)
- [Claude Code](#claude-code)
- [VS Code](#vs-code)
- [Cursor](#cursor)
- [Devin Desktop (formerly Windsurf)](#devin-desktop-formerly-windsurf)
- [Gemini CLI](#gemini-cli)
- [Codex](#codex)
- [Check that it works](#check-that-it-works)
- [The container image](#the-container-image)
- [Troubleshooting](#troubleshooting)

## Claude Desktop

**The easiest way:** download `tenable-docs-mcp_<version>.mcpb` from the [latest release](https://github.com/lanz/tenable-docs-mcp/releases/latest) and open it, or drag it onto **Settings > Extensions**. There's nothing to configure.

**Or, with a binary you installed:** choose **Settings > Developer > Edit Config**, and add the server to `claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "tenable-docs": {
      "command": "/path/to/tenable-docs-mcp"
    }
  }
}
```

Then quit and reopen Claude Desktop.

**The Developer Portal MCP:** choose **Settings > Connectors > Add custom connector**, and enter `https://developer.tenable.com/mcp`.

**The companion skill:** zip the [`skills/tenable-docs`](../skills/tenable-docs) folder, and upload it in Claude Desktop's settings, where Skills are listed.

## Claude Code

```bash
claude mcp add --scope user tenable-docs -- /path/to/tenable-docs-mcp
claude mcp add --scope user --transport http tenable-developer https://developer.tenable.com/mcp
```

`--scope user` makes them available in every project. `/mcp` shows both servers and their tools, and the server's prompts appear as slash commands, such as `/mcp__tenable-docs__how-to`.

**The companion skill:** copy the [`skills/tenable-docs`](../skills/tenable-docs) folder into `~/.claude/skills/` (every project) or `.claude/skills/` (one project). Claude Code loads it when a question is about a Tenable product.

## VS Code

In GitHub Copilot's agent mode, MCP servers come from `mcp.json`: `.vscode/mcp.json` in a workspace, or your user `mcp.json` (**MCP: Open User Configuration** in the Command Palette):

```json
{
  "servers": {
    "tenable-docs": {
      "type": "stdio",
      "command": "/path/to/tenable-docs-mcp"
    },
    "tenable-developer": {
      "type": "http",
      "url": "https://developer.tenable.com/mcp"
    }
  }
}
```

Start the servers from the **Start** link above each entry, or from **MCP: List Servers**. Choose **Agent** in the Chat view, and the tools appear under **Configure Tools**.

**The companion skill's guidance:** VS Code doesn't load Agent Skills, but you can paste the instructions from [`SKILL.md`](../skills/tenable-docs/SKILL.md) (the text after its front matter) into `.github/copilot-instructions.md`.

## Cursor

In `~/.cursor/mcp.json` (every project) or `.cursor/mcp.json` (one project):

```json
{
  "mcpServers": {
    "tenable-docs": {
      "command": "/path/to/tenable-docs-mcp"
    },
    "tenable-developer": {
      "url": "https://developer.tenable.com/mcp"
    }
  }
}
```

The servers appear in **Cursor Settings**, in its MCP section, where you can turn each on and off.

**The companion skill's guidance:** paste the instructions from [`SKILL.md`](../skills/tenable-docs/SKILL.md) into a project rule in `.cursor/rules/`.

## Devin Desktop (formerly Windsurf)

Windsurf is now called Devin Desktop. In its Agents window, open **Customize > MCPs**, choose **This computer**, and open the config file, or edit it directly: `~/.config/devin/mcp_config.json` (on Windows, `%APPDATA%\devin\mcp_config.json`):

```json
{
  "mcpServers": {
    "tenable-docs": {
      "command": "/path/to/tenable-docs-mcp"
    },
    "tenable-developer": {
      "url": "https://developer.tenable.com/mcp"
    }
  }
}
```

Refresh the list under **This computer** to load them. By default, Devin Desktop asks before each tool call.

Versions from before the rename read `~/.codeium/windsurf/mcp_config.json` instead, with `serverUrl` in place of `url` for remote servers.

**The companion skill's guidance:** paste the instructions from [`SKILL.md`](../skills/tenable-docs/SKILL.md) into `AGENTS.md` in your project.

## Gemini CLI

```bash
gemini mcp add -s user tenable-docs /path/to/tenable-docs-mcp
gemini mcp add -s user --transport http tenable-developer https://developer.tenable.com/mcp
```

Or add them to `~/.gemini/settings.json` (every project) or `.gemini/settings.json` (one project). Gemini CLI uses `httpUrl` for Streamable HTTP servers; `url` would mean the older SSE transport:

```json
{
  "mcpServers": {
    "tenable-docs": {
      "command": "/path/to/tenable-docs-mcp"
    },
    "tenable-developer": {
      "httpUrl": "https://developer.tenable.com/mcp"
    }
  }
}
```

`/mcp` in Gemini CLI lists the servers and their tools. `gemini mcp add` without `-s user` adds a server to the current project only.

**The companion skill's guidance:** paste the instructions from [`SKILL.md`](../skills/tenable-docs/SKILL.md) into `GEMINI.md` in your project, or in `~/.gemini/GEMINI.md` for every project.

## Codex

In `~/.codex/config.toml`, which the Codex CLI, the IDE extension, and the desktop app share:

```toml
[mcp_servers.tenable-docs]
command = '/path/to/tenable-docs-mcp'

[mcp_servers.tenable-developer]
url = 'https://developer.tenable.com/mcp'
```

Single quotes make TOML literal strings, so a Windows path keeps its backslashes as they are (`'C:\tools\tenable-docs-mcp.exe'`). For this server alone, `codex mcp add tenable-docs -- /path/to/tenable-docs-mcp` writes the same entry. `codex mcp list` shows the servers.

**The companion skill's guidance:** paste the instructions from [`SKILL.md`](../skills/tenable-docs/SKILL.md) into `AGENTS.md` in your project, or in `~/.codex/AGENTS.md` for every project.

## Check that it works

Ask the assistant something the docs answer, such as:

> How do I install Tenable Nessus on Linux?

A good answer gives the documented steps in order and links to the exact section of docs.tenable.com it came from, such as a link ending in `#…` on the Nessus install page. Most clients show the tool calls too: `search_docs`, then `read_doc`. An API question, such as "How do I launch a scan with the Tenable Vulnerability Management API?", should go to the Developer Portal MCP instead.

The first time the server starts, it downloads the documentation index, which takes a minute or two. Until then, its tools say the index is downloading. `index_status` (or `tenable-docs-mcp doctor` in a terminal) shows how it's going.

## The container image

Every client above can run the server from its container image instead of a binary. Use `docker` as the command, with these arguments:

```json
{
  "mcpServers": {
    "tenable-docs": {
      "command": "docker",
      "args": ["run", "-i", "--rm", "--mount", "type=volume,src=tenable-docs-mcp,dst=/data", "ghcr.io/lanz/tenable-docs-mcp"]
    }
  }
}
```

The volume keeps the index between runs, so it's downloaded once. In Codex, the same goes in TOML: `command = 'docker'` and `args = ['run', '-i', '--rm', '--mount', 'type=volume,src=tenable-docs-mcp,dst=/data', 'ghcr.io/lanz/tenable-docs-mcp']`.

## Troubleshooting

`tenable-docs-mcp doctor` checks the index, the network, and this program's path, and says what to fix. The most common problems:

| Symptom | Likely cause | Fix |
| --- | --- | --- |
| The client says the server failed to start | The path is wrong, or a Windows path in JSON has single backslashes | Run `tenable-docs-mcp config -client NAME` and paste its output, which has the right path and escaping |
| The tools say the index is downloading | The first download is still running | Wait a minute or two; `index_status` shows its progress |
| The download fails behind a corporate proxy | A TLS-inspecting proxy, or a proxy the server doesn't know about | `doctor` says which. Set `HTTPS_PROXY`, and install the proxy's certificate in the system store. Until then, the server reads the live sites instead. |
| Answers don't use the server | The client hasn't enabled the server's tools, or the assistant answered from memory | Check that the server is on (each section above says where), and ask the assistant to use the Tenable docs; the companion skill helps |
| A newer release is available | The server tells you through `index_status`, `doctor`, and your assistant | See [Updating](../README.md#updating) |
