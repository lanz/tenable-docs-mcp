# Tenable Docs MCP

An MCP server that gives AI assistants cited answers from Tenable's product documentation: how to do a task, what a setting does, which role it needs, what a system requires, and what changed in a release. It covers [docs.tenable.com](https://docs.tenable.com) and its seven translated sites, in every product, version, and language, and links each answer to the exact section.

> **In progress.** Tenable Docs MCP is working toward its 1.0 release. The tools, prompts, and resources below work, but the prebuilt index they search isn't published yet, so a new install has nothing to search. Once it's published, the server downloads it on first run and keeps it up to date. Until then, this README describes how 1.0 will work. There are no release binaries yet.

For questions about Tenable's APIs, use the [Tenable Developer Portal MCP](https://developer.tenable.com/mcp), which developer.tenable.com hosts. The two work together: this one covers the product documentation, and that one covers the API reference.

## What it does

- **Searches every docs site** (English, Japanese, Simplified and Traditional Chinese, Korean, French, German, and Spanish), in every product and version. It detects the question's language and falls back to English when a page isn't translated.
- **Answers for the right product and version.** It knows Tenable's product names, retired ones included (Tenable.io is now Tenable Vulnerability Management). It uses the current version unless the question names one, such as "Security Center 6.3".
- **Cites the exact section**, with a link to its heading, and points translations and older versions to the current English page.
- **Answers specific questions directly:** a setting's default, the role a task needs, ports and system requirements, release notes, and glossary terms.
- **Checks answers** before they're sent: `verify_citations` finds broken links and anchors, and links to older versions or translations.
- **Says when the docs don't cover something**, and can record the question. With the user's say-so, it sends the question, or page feedback, to Tenable's documentation team. The user writes it, or approves it, in a form.

## Tools

| Tool | What it does |
| --- | --- |
| `search_docs` | Ranked sections for a question, with snippets, product, version, language, and the required role. Filters for product, guide, guide type, version, language, release notes, and role. |
| `read_doc` | A section or page as Markdown, with where it is, the roles it needs, when it changed, and the same page in other languages and versions. Long content comes in parts. |
| `outline` | A page's headings, with links and word counts, or a guide's pages. |
| `browse_guide` | A guide's table of contents. |
| `related_pages` | A page's neighbors, its links in and out, same-titled pages in other products, and its other languages and versions. |
| `list_products` | The products, guides, and languages the index covers. |
| `find_setting` | A setting's description, options, and default, from the docs' settings tables. |
| `required_role` | The role or permissions a task needs. |
| `requirements` | Ports, hardware, operating systems, browsers, and other requirements. |
| `release_notes` | Release-note entries by product, version, date, and topic. |
| `define_term` | Glossary definitions, synonyms, and retired product names. |
| `verify_citations` | Checks every docs link in an answer or draft: that pages and anchors exist, and which links go to older versions or translations. |
| `report_gap` | Records a question the docs didn't answer, on this computer. Then, if the user wants, sends it to the documentation team. |
| `open_feedback_form` | Shows the user the docs feedback form for a page, and sends what they write. |
| `index_status` | The server's version, and what the index holds and how fresh it is. |
| `refresh_index` | Updates pages or a guide in the index from the live sites. |

Every tool returns structured results, with a summary for the assistant. The query tools are read-only.

## Prompts and resources

**Prompts** start common tasks. In Claude Code, they're slash commands, such as `/mcp__tenable-docs__how-to`.

| Prompt | Use it to |
| --- | --- |
| `how-to` | Get steps from the docs for a task |
| `explain-setting` | Explain a setting: what it does, its options, and its default |
| `troubleshoot` | Find what the docs say about a problem or error message |
| `whats-new` | Summarize a release |
| `who-can` | Find the role or permissions an action needs |
| `upgrade-check` | Check what changes between two versions before upgrading |
| `fact-check` | Check a reply, draft, or answer against the docs |

Their arguments complete products, guides, guide types, versions, and languages.

**Resources** let a client attach docs content directly:

- `tenable-docs://page/{doc_id}`: a page as Markdown, where `doc_id` is its address without `https://`, such as `docs.tenable.com/nessus/Content/GettingStarted.htm`
- `tenable-docs://guide/{guide}/toc`: a guide's table of contents, with `?language=` for a translation
- `tenable-docs://status`: the index status

## Install

Until the 1.0 release has binaries for Windows, macOS, and Linux, build it from source with Go 1.26 or later:

```bash
git clone https://github.com/lanz/tenable-docs-mcp
cd tenable-docs-mcp
go build ./cmd/tenable-docs-mcp
./tenable-docs-mcp version
```

## Connect it to your assistant

The server speaks MCP over stdio, so a client runs it as a command. Use the full path to the binary. On Windows, use forward slashes in shell commands, and doubled backslashes in JSON.

**Claude Code:**

```bash
claude mcp add --scope user tenable-docs -- /path/to/tenable-docs-mcp
```

**Claude Desktop:** in `claude_desktop_config.json` (Settings › Developer › Edit Config):

```json
{
  "mcpServers": {
    "tenable-docs": { "command": "/path/to/tenable-docs-mcp" }
  }
}
```

**VS Code:** in `.vscode/mcp.json`, or your user `mcp.json`:

```json
{
  "servers": {
    "tenable-docs": { "type": "stdio", "command": "/path/to/tenable-docs-mcp" }
  }
}
```

**Cursor:** in `~/.cursor/mcp.json`, use the same `mcpServers` entry as Claude Desktop.

Other MCP clients work the same way: run `tenable-docs-mcp` with no arguments.

To serve MCP over HTTP instead, for a client on the same machine:

```bash
tenable-docs-mcp serve -http 127.0.0.1:8080   # at http://127.0.0.1:8080/mcp
```

The HTTP server listens only on this machine, and refuses cross-origin browser requests and requests under other host names.

## Command line

| Command | What it does |
| --- | --- |
| `tenable-docs-mcp` | Serve MCP over stdio (what clients run) |
| `tenable-docs-mcp serve [-http ADDR] [-index DIR]` | Serve over stdio, or over HTTP on this machine, from the index in `DIR` |
| `tenable-docs-mcp verify-links [-base URL] [-live] [-json] FILE...` | Check the docs links in answers or drafts (`-` reads standard input). Exits 1 if any is broken. |
| `tenable-docs-mcp gaps export [-since DATE] [-json]` | Summarize the questions the docs didn't answer |
| `tenable-docs-mcp gaps clear -yes` | Delete the recorded questions |
| `tenable-docs-mcp index import [-sites en,jp] [-index DIR] SNAPSHOT` | Verify an index snapshot and build the index from it |
| `tenable-docs-mcp index verify SNAPSHOT` | Check a snapshot's files against its manifest |
| `tenable-docs-mcp index build -source "en=DIR;jp=DIR" [-out DIR \| -index DIR]` | Build the index, or a snapshot, from local copies of the sites |
| `tenable-docs-mcp version` | Print the version |

The index is in `tenable-docs-mcp/index` in your user cache folder, or in `$TDM_INDEX_DIR`.

## Privacy and security

- **No telemetry.** The server never reports how it's used.
- **What it connects to:**
  - the eight docs sites, to download the index, refresh pages, and check links live
  - Tenable's docs feedback service, only when the user sends feedback or a gap report from a form

  It refuses every other host.
- **What it sends to Tenable:** only what the user writes or approves in a feedback or gap form, and only when they accept it. Nothing is sent from clients that can't show forms. Sending is limited to one item a minute and ten a day. The assistant can't write feedback for the user.
- **What it stores, on this computer:**
  - the index, in the user cache folder
  - questions recorded with `report_gap` (`gaps.jsonl`) and a log of what was sent (`feedback.jsonl`), in the user settings folder, readable only by the user

Downloaded index files are checked against their SHA-256 checksums before they're used.

## How it works

Each docs page is parsed into sections that keep each heading's anchor, along with the page's product, guide type, version, and language. Each page also gets its place in its guide's table of contents, and the roles, settings, requirements, and release notes it describes.

The index is a set of SQLite databases with full-text search, one for each language's current and older-version pages. Each language gets the right kind of matching: stemming for English, accent folding for French, German, and Spanish, and trigrams for Japanese, Chinese, and Korean, which don't put spaces between words. Ranking prefers the guides that suit the question, such as release notes for "what's new" and requirements pages for ports.

The index is built from a snapshot of the docs, published on docs.tenable.com and verified when downloaded.

Search quality is measured: an evaluation set of questions across products, versions, and languages checks that the right page appears in the top five results. Today it does for 88% to 100% of questions, depending on the language and question set.

## Development

```bash
go build ./...
go vet ./...
go test ./...
```

Some tests can also run against a real index or the live sites. They're skipped unless their environment variables are set:

- `TDM_INDEX_DIR`: run the search evaluations against the index there
- `TDM_SITE_DIRS`: build from local copies of the sites (`en=DIR;jp=DIR`)
- `TDM_LIVE=1`: check against the live sites

## License

Copyright (C) 2026 [Lance Duvall](https://github.com/lanz).

Licensed under the [GNU General Public License v3.0](LICENSE) or later.

## Disclaimer

This is an independent open source community project. It is not an official Tenable product and is not affiliated with, sponsored by, endorsed by, or supported by Tenable, Inc.

Tenable, Nessus, and other Tenable product names are trademarks or registered trademarks of Tenable, Inc. Other names may be trademarks of their respective owners.
