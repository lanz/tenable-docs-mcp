// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lanz/tenable-docs-mcp/internal/gaps"
)

// gapEnv returns an Env that records gaps in a temporary file and sends to a fake
// endpoint, with the test index.
func gapEnv(t *testing.T) (*Env, *[]string, *time.Time) {
	t.Helper()
	env, got, now := feedbackEnv(t)
	env.Index = buildIndex(t)
	env.Engine = nil
	env.GapsFile = filepath.Join(t.TempDir(), "gaps.jsonl")
	return env, got, now
}

func recorded(t *testing.T, env *Env) []gaps.Record {
	t.Helper()
	recs, err := gaps.Read(env.GapsFile)
	if err != nil {
		t.Fatal(err)
	}
	return recs
}

func TestGapForm(t *testing.T) {
	for _, protocol := range []string{"", "2025-11-25"} {
		t.Run("protocol "+protocol, func(t *testing.T) { testGapForm(t, protocol) })
	}
}

func testGapForm(t *testing.T, protocol string) {
	question := "How do I scan printers?"
	args := map[string]any{"question": question, "product": "Nessus", "url": "docs.tenable.com/nessus/Content/Install.htm", "note": "only general scan pages"}
	for _, tc := range []struct {
		name   string
		answer *mcp.ElicitResult
		status string // "" means the call fails, after recording
		want   []string
	}{
		{"edited and sent", &mcp.ElicitResult{Action: "accept", Content: map[string]any{
			"question": "How do I scan network printers?", "details": "Our printers keep rebooting.", "email": "b@example.com"}},
			"sent", []string{`"type":"gap"`, `"question":"How do I scan network printers?"`,
				`"comment":"[Tenable Docs MCP test] Docs gap: How do I scan network printers?\n\nOur printers keep rebooting."`,
				`"email":"b@example.com"`, `"currentUrl":"` + feedbackPage + `"`, `"title":"Install Nessus on Linux"`, `"product":"Nessus"`}},
		{"sent as it was", &mcp.ElicitResult{Action: "accept", Content: map[string]any{"question": question}},
			"sent", []string{`"question":"How do I scan printers?"`}},
		{"question left out", &mcp.ElicitResult{Action: "accept"},
			"sent", []string{`"question":"How do I scan printers?"`}},
		{"declined", &mcp.ElicitResult{Action: "decline"}, "declined", nil},
		{"cancelled", &mcp.ElicitResult{Action: "cancel"}, "cancelled", nil},
		{"invalid email", &mcp.ElicitResult{Action: "accept", Content: map[string]any{"question": question, "email": "nope"}}, "", nil},
	} {
		env, got, _ := gapEnv(t)
		var asked *mcp.ElicitParams
		cs := connectEliciting(t, env, protocol, func(r *mcp.ElicitRequest) *mcp.ElicitResult {
			asked = r.Params
			return tc.answer
		})
		var out gapOut
		text, isErr := callRaw(t, cs, "report_gap", args, &out)
		switch {
		case tc.status == "" && (!isErr || !strings.Contains(text, "recorded on this computer, but not sent")):
			t.Errorf("%s: %q", tc.name, text)
		case tc.status != "" && (isErr || out.SentToTenable != tc.status || !out.Recorded || out.Total != 1):
			t.Errorf("%s: %q, %+v", tc.name, text, out)
		}
		// Recorded once, whatever the answer.
		if recs := recorded(t, env); len(recs) != 1 || recs[0].Question != question || recs[0].Note != "only general scan pages" {
			t.Errorf("%s: recorded %+v", tc.name, recs)
		}
		if tc.want == nil && len(*got) != 0 || tc.want != nil && len(*got) != 1 {
			t.Errorf("%s: sent %v", tc.name, *got)
		}
		for _, w := range tc.want {
			if len(*got) == 1 && !strings.Contains((*got)[0], w) {
				t.Errorf("%s: sent %s, want %s", tc.name, (*got)[0], w)
			}
		}
		// The note stays on this computer.
		if len(*got) == 1 && strings.Contains((*got)[0], "only general scan pages") {
			t.Errorf("%s: the note was sent", tc.name)
		}
		if tc.status == "sent" && (out.WrittenBy == "" || !strings.Contains(text, "showed them a form")) {
			t.Errorf("%s: %q, %+v", tc.name, text, out)
		}
		// The record has its page, and is marked sent only if it was, so gaps submit
		// doesn't offer it again.
		if recs := recorded(t, env); len(recs) == 1 {
			if recs[0].URL != feedbackPage {
				t.Errorf("%s: recorded page %q", tc.name, recs[0].URL)
			}
			if sent := !recs[0].SentAt.IsZero(); sent != (tc.status == "sent") {
				t.Errorf("%s: marked sent %v", tc.name, sent)
			}
		}
		// The form has the question filled in, and nothing required.
		schema, _ := json.Marshal(asked.RequestedSchema)
		if !strings.Contains(string(schema), `"default":"How do I scan printers?"`) || strings.Contains(string(schema), "required") ||
			!strings.Contains(asked.Message, "Page: "+feedbackPage) || !strings.Contains(asked.Message, "recorded on this computer either way") {
			t.Errorf("%s: form %s\n%s", tc.name, schema, asked.Message)
		}
	}
}

func TestGapWithoutForms(t *testing.T) {
	env, got, _ := gapEnv(t)
	var out gapOut
	text := call(t, connectEnv(t, env), "report_gap", map[string]any{"question": "Where are the logs?"}, &out)
	if out.SentToTenable != "no_form" || !out.Recorded || len(*got) != 0 || !strings.Contains(text, "can't show forms") {
		t.Errorf("%q, %+v", text, out)
	}
	// Without a sender, gaps are recorded as before, with no offer.
	env.Feedback = nil
	call(t, connectEnv(t, env), "report_gap", map[string]any{"question": "Where are the logs?"}, &out)
	if out.SentToTenable != "" || out.Total != 2 {
		t.Errorf("without a sender: %+v", out)
	}
}

func TestGapPage(t *testing.T) {
	env, _, _ := gapEnv(t)
	for _, tc := range []struct{ url, product, want string }{
		{"docs.tenable.com/nessus/Content/Ports.htm", "Nessus", en + "nessus/Content/Ports.htm"},
		{"", "Nessus", en + "nessus/Content/Welcome.htm"},
		{"https://example.com/x", "", docsHome},
		{"", "Identity Exposure", docsHome},
		{"", "", docsHome},
	} {
		if got := env.gapPage(ctx, tc.url, tc.product); got != tc.want {
			t.Errorf("gapPage(%q, %q) = %q, want %q", tc.url, tc.product, got, tc.want)
		}
	}
}

func TestGapAndFeedbackShareLimits(t *testing.T) {
	env, got, now := gapEnv(t)
	forms := 0
	cs := connectEliciting(t, env, "", func(*mcp.ElicitRequest) *mcp.ElicitResult {
		forms++
		return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"comment": "Clear.", "question": "Why?"}}
	})
	call(t, cs, "open_feedback_form", map[string]any{"url": feedbackPage, "helpful": true}, nil)
	// Over the limit, the gap is recorded, but no form is shown.
	var out gapOut
	text := call(t, cs, "report_gap", map[string]any{"question": "Why?"}, &out)
	if out.SentToTenable != "rate_limited" || !out.Recorded || !strings.Contains(text, "rate limited") || forms != 1 || len(*got) != 1 ||
		len(recorded(t, env)) != 1 {
		t.Errorf("%q, %+v, %d forms, %d sent", text, out, forms, len(*got))
	}
	*now = now.Add(2 * time.Minute)
	call(t, cs, "report_gap", map[string]any{"question": "Why?"}, &out)
	if out.SentToTenable != "sent" || len(*got) != 2 {
		t.Errorf("a minute later = %+v, %d sent", out, len(*got))
	}
}
