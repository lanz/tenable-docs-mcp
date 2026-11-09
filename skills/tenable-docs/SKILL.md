---
name: tenable-docs
description: Use before answering any question about a Tenable product, instead of answering from memory. That includes how to install, configure, or use Tenable Vulnerability Management (Tenable.io), Security Center (Tenable.sc), Nessus, Nessus Agents, Tenable One, OT Security, Identity Exposure, Cloud Security, Web App Scanning, or Attack Surface Management; their settings, required roles, system requirements, ports, licensing, and release notes; Tenable terms; checking a reply or draft against the docs; and any docs.tenable.com link. It answers from Tenable's product documentation through the Tenable Docs MCP server, with a link to the exact section.
user-invocable: true
metadata:
  version: "1.0.0"
  maintainer: Lance Duvall
---

# Answering from the Tenable docs

The Tenable Docs MCP server searches Tenable's product documentation: docs.tenable.com and its seven translated sites, in every product, guide, and published version. Answer from what it returns, cite the section you used, and say plainly when the docs don't cover something. Don't fill gaps from memory: product behavior changes between versions, and an uncited answer can't be checked.

## Before you answer

1. **Search first.** Call `search_docs` with the user's question in their own words and language. Pass `product`, `version`, or `language` only when the user gave them or the conversation makes them clear.
2. **Try again before giving up.** If the results don't answer the question, search two or three more times with different wording before saying the docs don't cover it:
   - the product's terms instead of the user's ("show potential false alarms" for "paranoid mode")
   - the task instead of the symptom ("configure a scan schedule" for "scan didn't run")
   - narrower or broader ("Nessus Agent linking" for "agents")
   - the spelling suggestions `search_docs` returns when nothing matches
3. **Read before you answer.** Open the best result with `read_doc`, passing its `url` with the `#anchor`. A snippet isn't enough to give steps from. For a long page, read the parts you need, or use `outline` to find the right section.
4. **Use the specific tools when they fit.** They return structured answers that a general search can miss:

   | Question | Tool |
   | --- | --- |
   | What a setting does, its default, or its valid values | `find_setting` |
   | Which role or permission a task needs | `required_role` |
   | Ports, hardware, operating systems, browsers, or dependencies | `requirements` |
   | What changed in a release | `release_notes` |
   | How a page or setting differs between two versions | `compare_versions` |
   | What changed in the docs recently | `whats_changed` |
   | What a term, abbreviation, or product name means | `define_term` |
   | What a guide covers | `browse_guide`, `list_products` |
   | Pages around one you've read | `related_pages` |

## Writing the answer

- **Give the documented steps in order,** as the page gives them, with their prerequisites. Don't merge, reorder, or skip steps, and don't add steps the page doesn't have. If you add advice of your own, label it as yours.
- **Cite the section,** using the `url` that `read_doc` returned, with its `#anchor`. Put the link next to what it supports.
- **Say what the answer is for:** the product, the version, and the language of the page you used.
  - `read_doc` and `search_docs` say when a page is for an older version or is a translation. For an older version, mention the current one (`current_url`). For a translation, `english_url` is the current English page; mention it when the translation may lag behind.
  - When it's ambiguous which product or version the user means, and the answer differs between them, **ask** before answering. Don't guess: a step that's right for Tenable Vulnerability Management can be wrong for Security Center.
- **Give the required role** when the docs state one. The header that `read_doc` returns lists it; `required_role` finds it for a task.
- **Quote exact UI labels, setting names, and values** as the docs give them, in bold or code, so the user can find them on screen.
- **Answer in the user's language.** If you used pages in another language, say so.

## Before you send it

Call `verify_citations` on your answer before sending it whenever it has docs links. Fix anything it reports as `not_found` or `anchor_missing` by searching again or citing the page without the anchor. Replace `older_version` links with the current page unless the user asked about that version.

## When the docs don't answer

- **Say so plainly.** Say what you searched for and what the closest pages cover, with their links, and don't present a guess as documented.
- **Offer to record the gap.** If the user agrees, call `report_gap` with the question in the user's words. It records the question on this computer, and, if the client can show forms, it lets the user send it to Tenable's documentation team. The user edits and sends that form; don't send it for them or describe it as sent.

## When a page looks wrong or unclear

If what you read seems to contradict another page, or the user says a page is wrong, out of date, or confusing, tell the user, and offer to open the page's feedback form. Call `open_feedback_form` only when the user wants to give feedback. **The feedback is theirs:** they write the comment in the form, and you don't suggest or write it for them. If the client can't show forms, give the user the page's link, where the same form is at the bottom of the page.

## API questions

For questions about Tenable's APIs (endpoints, parameters, request bodies, authentication, or rate limits), use the **Tenable Developer Portal MCP** (`https://developer.tenable.com/mcp`) when it's connected. If it isn't, say that it answers API questions, and give developer.tenable.com as the reference. Use this server for the product documentation the API calls relate to, such as what a setting means.

## Doing the task for the user

If the **Tenable Hexa AI MCP** is connected, you can offer to carry out the documented task in the user's Tenable environment, after answering. Describe what you would do, step by step, and do it **only after the user says yes**. Never change anything in their environment on your own initiative, and stop to ask again if a step would differ from what you described.

## When the tools return nothing

If a search or reading tool says the index isn't built, is being built, or has no docs for a language or version, call `index_status` and tell the user what it says. A first download takes a minute or two; reading the live sites instead takes longer. Don't answer from memory in the meantime.

## Example

> **User:** How do I give someone permission to launch scans in Tenable Vulnerability Management?
>
> 1. `search_docs` with "give a user permission to launch scans", and `product` "Tenable Vulnerability Management".
> 2. `read_doc` on the best section, and `required_role` for "launch scans" if the page doesn't state the role.
> 3. Answer with the documented steps in order, the role needed to do them, and the section's link.
> 4. `verify_citations` on the answer, then send it.
