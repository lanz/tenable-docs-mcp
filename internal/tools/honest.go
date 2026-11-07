// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lanz/tenable-docs-mcp/internal/cite"
	"github.com/lanz/tenable-docs-mcp/internal/gaps"
	"github.com/lanz/tenable-docs-mcp/internal/index"
	"github.com/lanz/tenable-docs-mcp/internal/refresh"
	"github.com/lanz/tenable-docs-mcp/internal/search"
	"github.com/lanz/tenable-docs-mcp/internal/site"
)

// maxVerifyText caps the text verify_citations reads.
const maxVerifyText = 200_000

type verifyIn struct {
	Text      string `json:"text" jsonschema:"the answer or draft to check, in Markdown, HTML, or plain text"`
	BaseURL   string `json:"base_url,omitempty" jsonschema:"the page the text will be published as, to resolve relative links (for a draft topic)"`
	CheckLive bool   `json:"check_live,omitempty" jsonschema:"also check the live docs sites, for anchors that aren't headings and pages the index doesn't have"`
}

type checkedLink struct {
	URL        string `json:"url"`
	Raw        string `json:"written_as,omitempty" jsonschema:"the link as written in the text"`
	Lines      []int  `json:"lines"`
	Status     string `json:"status" jsonschema:"ok, older_version, translation, anchor_unknown, anchor_missing, moved, not_found, or not_indexed"`
	Problem    bool   `json:"problem" jsonschema:"set when the link is broken: not_found or anchor_missing"`
	Title      string `json:"title,omitempty"`
	Current    string `json:"current_url,omitempty" jsonschema:"the current version, for an older version or a moved page"`
	English    string `json:"english_url,omitempty" jsonschema:"the current English page, to cite instead of a translation or an older version"`
	Suggestion *link  `json:"suggestion,omitempty" jsonschema:"a page that may be what a broken link meant"`
	Note       string `json:"note,omitempty"`
}

type verifyOut struct {
	Links    []checkedLink `json:"links"`
	Problems int           `json:"problems"`
	Warnings int           `json:"warnings" jsonschema:"older versions, translations, anchors that aren't headings, moved pages, and links the index can't check"`
	Skipped  cite.Skipped  `json:"skipped"`
}

type gapIn struct {
	Question string `json:"question" jsonschema:"the user's question, as asked, without personal or customer details"`
	Product  string `json:"product,omitempty"`
	Language string `json:"language,omitempty"`
	Note     string `json:"note,omitempty" jsonschema:"what was searched and found, such as only the 6.3 docs cover it; kept on this computer only"`
	URL      string `json:"url,omitempty" jsonschema:"the docs page that came closest to answering, if any"`
}

type gapOut struct {
	Recorded bool   `json:"recorded"`
	Total    int    `json:"total" jsonschema:"how many questions are recorded"`
	File     string `json:"file"`
	// SentToTenable says what happened to the offer to send the gap to Tenable.
	SentToTenable string `json:"sent_to_tenable,omitempty" jsonschema:"sent, declined, cancelled, no_form (this client can't show forms, so it wasn't offered), or rate_limited (too many sent recently, so it wasn't offered); empty when sending isn't available"`
	Sent          string `json:"sent,omitempty" jsonschema:"exactly what was sent, as the user approved it in the form"`
	WrittenBy     string `json:"written_by,omitempty" jsonschema:"who approved what was sent: always the user, in the form"`
}

type refreshIn struct {
	URLs     []string `json:"urls,omitempty" jsonschema:"pages to refresh, at most 500"`
	Guide    string   `json:"guide,omitempty" jsonschema:"a guide's path (nessus, security-center/6_8), to refresh its table of contents and every page; give urls or guide"`
	Language string   `json:"language,omitempty" jsonschema:"the guide's language (default en)"`
}

type refreshOut struct {
	refresh.Report
}

// CheckLinks verifies the documentation links in a text, as verify_citations and the
// verify-links command do.
func CheckLinks(ctx context.Context, env *Env, text, base string, live bool) (verifyOut, error) {
	if err := env.ready(); err != nil {
		return verifyOut{}, err
	}
	if len(text) > maxVerifyText {
		return verifyOut{}, fmt.Errorf("the text is longer than %d characters", maxVerifyText)
	}
	found, skipped := cite.Extract(text, base)
	c := &cite.Checker{Index: env.Index, Engine: env.Engine}
	if live {
		if env.Live == nil {
			return verifyOut{}, errors.New("live checks aren't available in this mode")
		}
		c.Live = env.Live
	}
	results, err := c.Check(ctx, found)
	if err != nil {
		return verifyOut{}, err
	}
	out := verifyOut{Links: []checkedLink{}, Skipped: skipped}
	for _, r := range results {
		cl := checkedLink{URL: r.URL, Raw: r.Raw, Lines: r.Lines, Status: r.Status, Problem: cite.Problem(r.Status), Title: r.Title,
			Current: r.Current, English: r.English, Note: r.Note}
		if r.Suggestion != nil {
			cl.Suggestion = &link{Title: r.Suggestion.Title, URL: r.Suggestion.URL}
		}
		switch {
		case cl.Problem:
			out.Problems++
		case r.Status != cite.OK:
			out.Warnings++
		}
		out.Links = append(out.Links, cl)
	}
	return out, nil
}

// NoteUnpublished marks broken relative links whose target exists beside the draft at
// path: a topic in the source project that isn't published (yet).
func NoteUnpublished(out *verifyOut, path string) {
	dir := filepath.Dir(path)
	for i := range out.Links {
		l := &out.Links[i]
		if l.Status != cite.NotFound || l.Raw == "" || strings.Contains(l.Raw, "://") || strings.HasPrefix(l.Raw, "/") {
			continue
		}
		target, _, _ := strings.Cut(l.Raw, "#")
		target, _, _ = strings.Cut(target, "?")
		if target == "" {
			continue
		}
		if unescaped, err := url.PathUnescape(target); err == nil {
			target = unescaped
		}
		if info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(target))); err == nil && !info.IsDir() {
			note := "the topic is in the source project but isn't published"
			if l.Note != "" {
				note = l.Note + "; " + note
			}
			l.Note = note
		}
	}
}

// VerifySummary writes a check's results as text.
func VerifySummary(out verifyOut) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d docs links: %d broken, %d to review.", len(out.Links), out.Problems, out.Warnings)
	if s := out.Skipped; s.Other+s.Relative+s.Files > 0 {
		fmt.Fprintf(&b, " Not checked: %d to other sites, %d files", s.Other, s.Files)
		if s.Relative > 0 {
			fmt.Fprintf(&b, ", %d relative (give the page's own URL to check them)", s.Relative)
		}
		b.WriteString(".")
	}
	for _, l := range out.Links {
		if l.Status == cite.OK {
			continue
		}
		mark := "!"
		if l.Problem {
			mark = "✗"
		}
		fmt.Fprintf(&b, "\n%s %s (line %s): %s", mark, l.URL, joinInts(l.Lines), strings.ReplaceAll(l.Status, "_", " "))
		if l.Note != "" {
			fmt.Fprintf(&b, "; %s", l.Note)
		}
		if l.Current != "" {
			fmt.Fprintf(&b, "\n  current: %s", l.Current)
		}
		if l.English != "" {
			fmt.Fprintf(&b, "\n  English: %s", l.English)
		}
		if l.Suggestion != nil {
			fmt.Fprintf(&b, "\n  maybe: %s %s", l.Suggestion.Title, l.Suggestion.URL)
		}
	}
	return b.String()
}

func joinInts(ns []int) string {
	var s []string
	for _, n := range ns {
		s = append(s, fmt.Sprint(n))
	}
	return strings.Join(s, ", ")
}

func registerHonest(s *mcp.Server, env *Env) {
	open := true
	mcp.AddTool(s, &mcp.Tool{
		Name: "verify_citations",
		Description: "Check the docs links in an answer or a draft before relying on it: that each page exists and its " +
			"anchor is on the page, which links go to older versions or translations (with the current English page " +
			"to cite instead), and suggestions for broken links. Run it on an answer before sending it.",
		Annotations: &mcp.ToolAnnotations{Title: "Verify citations", ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &open},
	}, func(ctx context.Context, req *mcp.CallToolRequest, in verifyIn) (*mcp.CallToolResult, verifyOut, error) {
		out, err := CheckLinks(ctx, env, in.Text, in.BaseURL, in.CheckLive)
		if err != nil {
			return nil, verifyOut{}, err
		}
		return summary("%s", VerifySummary(out)), out, nil
	})

	notDestructive := false
	mcp.AddTool(s, &mcp.Tool{
		Name: "report_gap",
		Description: "Record a question the docs didn't answer, in a file on this computer that only the user can " +
			"read. Then, if the user's client can show forms, the user is offered a form to send it to Tenable's " +
			"documentation team: the question filled in for them to edit, with optional details and email. Nothing " +
			"is sent unless they accept. Pass url, the docs page that came closest to answering, if any. The user " +
			"can also summarize the file with `tenable-docs-mcp gaps export`. Leave out personal and customer details.",
		Annotations: &mcp.ToolAnnotations{Title: "Report a gap", DestructiveHint: &notDestructive, OpenWorldHint: &open},
	}, func(ctx context.Context, req *mcp.CallToolRequest, in gapIn) (*mcp.CallToolResult, gapOut, error) {
		if env.GapsFile == "" {
			return nil, gapOut{}, errors.New("recording gaps isn't available in this mode")
		}
		product := ""
		if in.Product != "" {
			if p, ok := search.ProductNamed(in.Product); ok {
				product = p
			} else {
				product = in.Product
			}
		}
		lang := in.Language
		if lang == "" {
			lang = search.DetectLanguage(in.Question)
		} else if l, ok := search.SiteFor(lang); ok {
			lang = l
		}
		now := env.now()
		if req.Params.InputResponses != nil {
			// The user's answer to the form below; the gap was recorded on the first call.
			return env.sendGap(ctx, req, env.gapPage(ctx, in.URL, product), in.Question, now)
		}
		n, err := gaps.Add(env.GapsFile, gaps.Record{Question: in.Question, Product: product, Language: lang, Note: in.Note, Version: env.Info.Version})
		if err != nil {
			return nil, gapOut{}, err
		}
		out := gapOut{Recorded: true, Total: n, File: env.GapsFile}
		text := fmt.Sprintf("Recorded. %d questions are recorded in %s; `tenable-docs-mcp gaps export` summarizes them.", n, env.GapsFile)
		if env.Feedback == nil {
			return summary("%s", text), out, nil
		}
		if !canElicit(req) {
			out.SentToTenable = "no_form"
			return summary("%s This client can't show forms, so it wasn't offered to Tenable's documentation team; "+
				"the user can share the summary instead.", text), out, nil
		}
		// Don't offer a form that can't be sent now.
		if err := env.Feedback.Check(); err != nil {
			out.SentToTenable = "rate_limited"
			return summary("%s It wasn't offered to Tenable's documentation team: %v. The user can report it again later, "+
				"or share the summary.", text, err), out, nil
		}
		// Offer to send it; the SDK calls this tool again with the user's answer.
		page := env.gapPage(ctx, in.URL, product)
		return &mcp.CallToolResult{
			InputRequests: mcp.InputRequestMap{gapFormID: gapForm(page, strings.TrimSpace(in.Question), env.Info.Version)},
			RequestState:  gapState(page, now),
		}, gapOut{}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "refresh_index",
		Description: "Update pages in the documentation index from the live docs sites: given pages, or every page of " +
			"a guide and its table of contents. Unchanged pages cost little; pages the sites no longer publish are " +
			"removed. Use it when a page seems out of date.",
		Annotations: &mcp.ToolAnnotations{Title: "Refresh the index", DestructiveHint: &notDestructive, IdempotentHint: true, OpenWorldHint: &open},
	}, func(ctx context.Context, req *mcp.CallToolRequest, in refreshIn) (*mcp.CallToolResult, refreshOut, error) {
		if err := env.ready(); err != nil {
			return nil, refreshOut{}, err
		}
		if !env.AllowRefresh || env.Live == nil {
			return nil, refreshOut{}, errors.New("refreshing the index isn't available in this mode")
		}
		r := &refresh.Refresher{Index: env.Index, Source: env.Live, Progress: progressFor(ctx, req, "Checking pages")}
		var rep refresh.Report
		var err error
		switch {
		case len(in.URLs) > 0 && in.Guide != "":
			return nil, refreshOut{}, errors.New("give urls or guide, not both")
		case len(in.URLs) > 0:
			var urls []string
			for _, u := range in.URLs {
				n, err := normalizeURL(u)
				if err != nil {
					return nil, refreshOut{}, err
				}
				urls = append(urls, n)
			}
			rep, err = r.Pages(ctx, urls)
		case in.Guide != "":
			code := "en"
			if in.Language != "" {
				l, ok := search.SiteFor(in.Language)
				if !ok {
					return nil, refreshOut{}, fmt.Errorf("no documentation in %q", in.Language)
				}
				code = l
			}
			path := strings.Trim(in.Guide, "/ ")
			g, _ := site.GuideOf("https://docs.tenable.com/" + path + "/Content/x.htm")
			rep, err = r.Guide(ctx, index.Key{Site: code, Older: g.Version != ""}, path)
		default:
			return nil, refreshOut{}, errors.New("give urls (pages) or guide (a guide's path)")
		}
		if err != nil {
			return nil, refreshOut{}, err
		}
		text := fmt.Sprintf("Checked %d pages: %d updated, %d unchanged, %d removed, %d skipped.", rep.Checked, rep.Updated, rep.Unchanged, rep.Removed, rep.Skipped)
		if rep.TocUpdated {
			text += " The table of contents was updated."
		}
		for _, f := range rep.Failed {
			text += "\nFailed: " + f
			env.logf(ctx, req.Session, "warning", "refresh failed: %s", f)
		}
		env.logf(ctx, req.Session, "info", "%s", text)
		return summary("%s", text), refreshOut{rep}, nil
	})
}
