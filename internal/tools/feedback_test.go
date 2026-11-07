// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lanz/tenable-docs-mcp/internal/feedback"
)

// feedbackEnv returns an Env whose feedback goes to a fake endpoint, and what it received.
func feedbackEnv(t *testing.T) (*Env, *[]string, *time.Time) {
	t.Helper()
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = append(got, string(b))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	}))
	t.Cleanup(srv.Close)
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	env := NewEnv(Info{Version: "test"}, nil)
	env.Feedback = &feedback.Sender{Endpoint: srv.URL, UserAgent: "test", LogFile: filepath.Join(t.TempDir(), "feedback.jsonl"), Now: func() time.Time { return now }}
	env.Now = func() time.Time { return now }
	return env, &got, &now
}

// connectEliciting connects a client that answers elicitations with answer.
func connectEliciting(t *testing.T, env *Env, protocol string, answer func(*mcp.ElicitRequest) *mcp.ElicitResult) *mcp.ClientSession {
	t.Helper()
	s := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "0"}, nil)
	Register(s, env)
	st, ct := mcp.NewInMemoryTransports()
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	opts := &mcp.ClientOptions{ElicitationHandler: func(_ context.Context, r *mcp.ElicitRequest) (*mcp.ElicitResult, error) { return answer(r), nil }}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, opts).Connect(ctx, ct, &mcp.ClientSessionOptions{ProtocolVersion: protocol})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

const feedbackPage = "https://docs.tenable.com/nessus/Content/Install.htm"

func TestSendFeedbackWithoutForms(t *testing.T) {
	env, got, _ := feedbackEnv(t)
	cs := connectEnv(t, env)
	var out feedbackOut
	text := call(t, cs, "open_feedback_form", map[string]any{"url": "docs.tenable.com/nessus/Content/Install.htm", "helpful": false}, &out)
	if out.Status != "no_form" || out.Page != feedbackPage || len(*got) != 0 || !strings.Contains(text, "Nothing was sent") || !strings.Contains(text, feedbackPage) {
		t.Errorf("out = %+v\n%s", out, text)
	}
	// The agent can't supply the comment or email.
	for _, a := range []map[string]any{
		{"url": feedbackPage, "helpful": false, "comment": "Words the user didn't write."},
		{"url": feedbackPage, "helpful": true, "email": "a@example.com"},
		{"url": "https://example.com/x", "helpful": true},
	} {
		if text, isErr := callRaw(t, cs, "open_feedback_form", a, nil); !isErr {
			t.Errorf("%v should fail: %s", a, text)
		}
	}
	if len(*got) != 0 {
		t.Errorf("sent %v", *got)
	}
	// Off without a sender (hosted mode).
	if text, isErr := callRaw(t, connect(t, nil), "open_feedback_form", map[string]any{"url": feedbackPage, "helpful": true}, nil); !isErr || !strings.Contains(text, "isn't available") {
		t.Errorf("off = %q", text)
	}
}

func TestSendFeedbackForm(t *testing.T) {
	// The current protocol asks through input requests; older ones, with a request
	// from the server.
	for _, protocol := range []string{"", "2025-11-25"} {
		t.Run("protocol "+protocol, func(t *testing.T) { testForm(t, protocol) })
	}
}

func testForm(t *testing.T, protocol string) {
	type form struct {
		message string
		schema  string
	}
	for _, tc := range []struct {
		name    string
		helpful bool
		answer  *mcp.ElicitResult
		status  string // "" means the call fails
		body    string // what the fake endpoint must receive
	}{
		{"negative", false, &mcp.ElicitResult{Action: "accept", Content: map[string]any{"comment": " My own words. ", "email": "b@example.com"}}, "sent",
			`{"currentUrl":"` + feedbackPage + `","helpful":"no","comment":"[Tenable Docs MCP test] My own words.","email":"b@example.com","source":"tenable-docs-mcp","type":"feedback","language":"en"}`},
		{"positive, nothing typed", true, &mcp.ElicitResult{Action: "accept"}, "sent",
			`{"currentUrl":"` + feedbackPage + `","helpful":"yes","source":"tenable-docs-mcp","type":"feedback","language":"en"}`},
		{"positive, blank fields", true, &mcp.ElicitResult{Action: "accept", Content: map[string]any{"comment": "", "email": ""}}, "sent",
			`{"currentUrl":"` + feedbackPage + `","helpful":"yes","source":"tenable-docs-mcp","type":"feedback","language":"en"}`},
		{"negative, blank comment", false, &mcp.ElicitResult{Action: "accept", Content: map[string]any{"comment": "   "}}, "", ""},
		{"invalid email", true, &mcp.ElicitResult{Action: "accept", Content: map[string]any{"email": "not an email"}}, "", ""},
		{"declined", false, &mcp.ElicitResult{Action: "decline"}, "declined", ""},
		{"cancelled", true, &mcp.ElicitResult{Action: "cancel"}, "cancelled", ""},
	} {
		env, got, _ := feedbackEnv(t)
		var f form
		cs := connectEliciting(t, env, protocol, func(r *mcp.ElicitRequest) *mcp.ElicitResult {
			s, _ := json.Marshal(r.Params.RequestedSchema)
			f = form{r.Params.Message, string(s)}
			return tc.answer
		})
		var out feedbackOut
		text, isErr := callRaw(t, cs, "open_feedback_form", map[string]any{"url": feedbackPage, "helpful": tc.helpful}, &out)
		switch {
		case tc.status == "" && !isErr:
			t.Errorf("%s: should fail: %+v", tc.name, out)
		case tc.status != "" && (isErr || out.Status != tc.status):
			t.Errorf("%s: %q, %+v", tc.name, text, out)
		}
		if tc.body == "" && len(*got) != 0 || tc.body != "" && (len(*got) != 1 || (*got)[0] != tc.body) {
			t.Errorf("%s: sent %v", tc.name, *got)
		}
		if tc.status == "sent" && (!strings.Contains(out.Sent, "Page: "+feedbackPage) || out.WrittenBy == "" || !strings.Contains(text, "showed them a feedback form")) {
			t.Errorf("%s: result = %+v", tc.name, out)
		}
		// The form shows the page and answer, starts empty, and requires a comment only
		// for "not helpful".
		answer := map[bool]string{true: "Helpful: yes", false: "Helpful: no"}[tc.helpful]
		if !strings.Contains(f.message, answer) || !strings.Contains(f.message, feedbackPage) || !strings.Contains(f.message, "documentation only") ||
			!strings.Contains(f.schema, `"comment"`) || !strings.Contains(f.schema, `"email"`) || strings.Contains(f.schema, "default") ||
			strings.Contains(f.schema, `"required"`) == tc.helpful {
			t.Errorf("%s: form = %+v", tc.name, f)
		}
	}
}

func TestSendFeedbackRateLimit(t *testing.T) {
	env, got, now := feedbackEnv(t)
	forms := 0
	cs := connectEliciting(t, env, "", func(*mcp.ElicitRequest) *mcp.ElicitResult {
		forms++
		return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"comment": "Clear."}}
	})
	args := map[string]any{"url": feedbackPage, "helpful": true}
	call(t, cs, "open_feedback_form", args, nil)
	// Within the minute, the user isn't asked to fill in a form that can't be sent.
	var out feedbackOut
	if text := call(t, cs, "open_feedback_form", args, &out); out.Status != "rate_limited" || !strings.Contains(text, "rate limited") || forms != 1 || len(*got) != 1 {
		t.Errorf("within a minute = %q, %+v, %d forms", text, out, forms)
	}
	*now = now.Add(2 * time.Minute)
	call(t, cs, "open_feedback_form", args, nil)
	if len(*got) != 2 {
		t.Errorf("sent %d", len(*got))
	}
}

func TestFormState(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	state := formState(feedbackPage, false, now)
	if err := checkFormState(state, feedbackPage, false, now.Add(time.Minute)); err != nil {
		t.Errorf("valid state: %v", err)
	}
	for name, err := range map[string]error{
		"other page":    checkFormState(state, feedbackPage+"x", false, now),
		"other answer":  checkFormState(state, feedbackPage, true, now),
		"expired":       checkFormState(state, feedbackPage, false, now.Add(formTTL+time.Minute)),
		"made up":       checkFormState("made-up", feedbackPage, false, now),
		"empty":         checkFormState("", feedbackPage, false, now),
		"from the past": checkFormState(formState(feedbackPage, false, now.Add(time.Hour)), feedbackPage, false, now),
	} {
		if err == nil {
			t.Errorf("%s should fail", name)
		}
	}
}

func TestSendFeedbackPageDetails(t *testing.T) {
	env, got, _ := feedbackEnv(t)
	env.Index = buildIndex(t)
	cs := connectEliciting(t, env, "", func(*mcp.ElicitRequest) *mcp.ElicitResult {
		return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"comment": "Clear."}}
	})
	call(t, cs, "open_feedback_form", map[string]any{"url": feedbackPage + "#Steps", "helpful": true}, nil)
	if len(*got) != 1 {
		t.Fatalf("sent %v", *got)
	}
	for _, want := range []string{`"title":"Install Nessus on Linux"`, `"product":"Nessus"`, `"guideType":"User Guide"`, `"language":"en"`, `"type":"feedback"`} {
		if !strings.Contains((*got)[0], want) {
			t.Errorf("sent %s, want %s", (*got)[0], want)
		}
	}
}
