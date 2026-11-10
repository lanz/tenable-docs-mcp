// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Arguments the prompts share.
var (
	argProduct   = &mcp.PromptArgument{Name: "product", Title: "Product", Description: "The Tenable product, by any of its names"}
	argVersion   = &mcp.PromptArgument{Name: "version", Title: "Version", Description: "The product version, such as 10.8 (default: the current one)"}
	argLanguage  = &mcp.PromptArgument{Name: "language", Title: "Language", Description: "The docs' language, as a site code such as en, jp, or fr (default: the question's language)"}
	argGuideType = &mcp.PromptArgument{Name: "guide_type", Title: "Guide type", Description: "Only this kind of guide, such as User Guide"}
)

// prompt is a prompt with the instructions it gives, from its arguments.
type prompt struct {
	mcp.Prompt
	text func(a args) string
}

type args map[string]string

// get returns an argument, trimmed.
func (a args) get(name string) string { return strings.TrimSpace(a[name]) }

// scope says which product, version, and language a prompt is about.
func (a args) scope() string {
	var b strings.Builder
	if p := a.get("product"); p != "" {
		fmt.Fprintf(&b, " The product is %s.", p)
	}
	if v := a.get("version"); v != "" {
		fmt.Fprintf(&b, " The version is %s.", v)
	}
	if g := a.get("guide_type"); g != "" {
		fmt.Fprintf(&b, " Look in the %s guides.", g)
	}
	if l := a.get("language"); l != "" {
		fmt.Fprintf(&b, " Use the %s docs, and answer in that language.", l)
	}
	return b.String()
}

// The steps every answer ends with.
const answerRules = " Cite the docs links you used, and check them with verify_citations before answering. " +
	"Say which product and version your answer is for. If the docs don't cover something, say so rather than " +
	"answering from memory" + gapRule + "."

// gapRule ends answerRules where report_gap is available, which a hosted server leaves
// out.
const gapRule = ", and offer to record it with report_gap"

var prompts = []prompt{
	{
		Prompt: mcp.Prompt{Name: "how-to", Title: "How do I…",
			Description: "Get steps from the Tenable docs for a task.",
			Arguments: []*mcp.PromptArgument{{Name: "task", Title: "Task", Description: "What you want to do", Required: true},
				argProduct, argVersion, argGuideType, argLanguage}},
		text: func(a args) string {
			return fmt.Sprintf("How do I %s?%s\n\nFind the answer in the Tenable documentation: search with search_docs, "+
				"passing the task in my words, and read the best results with read_doc. Give the steps the docs give, in "+
				"order, with the role or permissions they need and anything to do first.", a.get("task"), a.scope()) + answerRules
		},
	},
	{
		Prompt: mcp.Prompt{Name: "explain-setting", Title: "Explain a setting",
			Description: "Explain a setting from the Tenable docs: what it does, its options, and its default.",
			Arguments: []*mcp.PromptArgument{{Name: "setting", Title: "Setting", Description: "The setting's name, as the product shows it", Required: true},
				argProduct, argVersion, argLanguage}},
		text: func(a args) string {
			return fmt.Sprintf("Explain the %q setting.%s\n\nLook it up with find_setting, and read the page it's on with "+
				"read_doc. Say what it does, its options and default, where to find it, and when to change it.", a.get("setting"), a.scope()) + answerRules
		},
	},
	{
		Prompt: mcp.Prompt{Name: "troubleshoot", Title: "Troubleshoot a problem",
			Description: "Find what the Tenable docs say about a problem or error message.",
			Arguments: []*mcp.PromptArgument{{Name: "problem", Title: "Problem", Description: "What's wrong, or the error message", Required: true},
				argProduct, argVersion, argLanguage}},
		text: func(a args) string {
			return fmt.Sprintf("Help me troubleshoot this: %s%s\n\nSearch the Tenable documentation with search_docs for "+
				"the symptom or exact error message, for troubleshooting pages, and, with release_notes, for known issues "+
				"and fixes. Read the best results with read_doc. Give likely causes and the docs' fixes, most likely first, "+
				"and what to check before each.", a.get("problem"), a.scope()) + answerRules
		},
	},
	{
		Prompt: mcp.Prompt{Name: "whats-new", Title: "What's new",
			Description: "Summarize a release from the Tenable release notes.",
			Arguments: []*mcp.PromptArgument{{Name: "product", Title: "Product", Description: "The Tenable product, by any of its names", Required: true},
				{Name: "version", Title: "Version", Description: "The release (default: the latest)"}, argLanguage}},
		text: func(a args) string {
			v := a.get("version")
			if v == "" {
				v = "the latest release"
			}
			return fmt.Sprintf("What's new in %s %s?%s\n\nUse release_notes for that release, and read its notes with "+
				"read_doc. Summarize new features, changes, fixed issues, known issues, and anything removed or "+
				"deprecated, with links to each.", a.get("product"), v, a.scope()) + answerRules
		},
	},
	{
		Prompt: mcp.Prompt{Name: "who-can", Title: "Who can…",
			Description: "Find which role or permissions the Tenable docs say an action needs.",
			Arguments: []*mcp.PromptArgument{{Name: "action", Title: "Action", Description: "What someone wants to do", Required: true},
				argProduct, argVersion, argLanguage}},
		text: func(a args) string {
			return fmt.Sprintf("Which role or permissions does someone need to %s?%s\n\nUse required_role, and read the "+
				"pages it finds with read_doc. Give the roles or permissions the docs name, and where they're assigned.",
				a.get("action"), a.scope()) + answerRules
		},
	},
	{
		Prompt: mcp.Prompt{Name: "upgrade-check", Title: "Check an upgrade",
			Description: "Check what changes between two versions before upgrading, from the Tenable docs.",
			Arguments: []*mcp.PromptArgument{{Name: "product", Title: "Product", Description: "The Tenable product, by any of its names", Required: true},
				{Name: "from_version", Title: "From version", Description: "The version you have", Required: true},
				{Name: "to_version", Title: "To version", Description: "The version to upgrade to (default: the latest)"}, argLanguage}},
		text: func(a args) string {
			to := a.get("to_version")
			if to == "" {
				to = "the latest version"
			}
			return fmt.Sprintf("I'm upgrading %s from %s to %s.%s\n\nUse release_notes for every release after %s up to %s, "+
				"and requirements for both versions. List what to know before upgrading: changed system or license "+
				"requirements, removed or deprecated features, changed defaults, upgrade steps and supported upgrade paths, "+
				"and known issues, with the release each comes from.", a.get("product"), a.get("from_version"), to, a.scope(),
				a.get("from_version"), to) + answerRules
		},
	},
	{
		Prompt: mcp.Prompt{Name: "fact-check", Title: "Fact-check against the Tenable docs",
			Description: "Check a reply, draft, or answer about a Tenable product against the docs: which claims are " +
				"right, wrong, or unsupported, with links and corrections.",
			Arguments: []*mcp.PromptArgument{{Name: "text", Title: "Text", Description: "The text to check, with its links", Required: true},
				{Name: "question", Title: "Question", Description: "The question it answers, if any"}}},
		text: func(a args) string {
			var b strings.Builder
			if q := a.get("question"); q != "" {
				fmt.Fprintf(&b, "Question: %s\n\n", q)
			}
			fmt.Fprintf(&b, "Text to check:\n\n%s\n\nFact-check this text against the Tenable documentation. First run "+
				"verify_citations on it, for broken, older-version, or translated links. Then check each claim with "+
				"search_docs and read_doc. List what's correct, what's wrong or out of date, and what the docs don't "+
				"support, each with the docs link that shows it, and suggest corrections.", a.get("text"))
			return b.String()
		},
	},
}

func registerPrompts(s *mcp.Server, env *Env) {
	for _, p := range prompts {
		s.AddPrompt(&p.Prompt, func(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			a := args{}
			for k, v := range req.Params.Arguments {
				a[k] = v
			}
			for _, arg := range p.Arguments {
				if arg.Required && a.get(arg.Name) == "" {
					return nil, fmt.Errorf("the %s prompt needs %s", p.Name, arg.Name)
				}
			}
			text := p.text(a)
			if env.Hosted {
				text = strings.Replace(text, gapRule, "", 1)
			}
			return &mcp.GetPromptResult{
				Description: p.Description,
				Messages:    []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: text}}},
			}, nil
		})
	}
}
