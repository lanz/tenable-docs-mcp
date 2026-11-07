// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
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

func TestSendFeedbackPreview(t *testing.T) {
	env, got, now := feedbackEnv(t)
	cs := connectEnv(t, env)
	args := map[string]any{"url": "docs.tenable.com/nessus/Content/Install.htm", "helpful": false, "comment": "The RHEL 10 steps are missing."}
	var out feedbackOut
	text := call(t, cs, "send_feedback", args, &out)
	if out.Status != "preview" || out.Token == "" || len(*got) != 0 ||
		!strings.Contains(text, "[Tenable Docs MCP test] The RHEL 10 steps are missing.") || !strings.Contains(text, "documentation only") {
		t.Fatalf("preview = %+v\n%s", out, text)
	}
	token := out.Token

	// A changed field, or a made-up token, doesn't send.
	changed := map[string]any{"url": feedbackPage, "helpful": false, "comment": "Something else.", "confirm_token": token}
	for _, a := range []map[string]any{changed, {"url": feedbackPage, "helpful": false, "comment": "The RHEL 10 steps are missing.", "confirm_token": "made-up"}} {
		if text, isErr := callRaw(t, cs, "send_feedback", a, nil); !isErr {
			t.Errorf("%v should fail: %s", a, text)
		}
	}
	// An approval request's state doesn't work as a preview token.
	approval := map[string]any{"url": feedbackPage, "helpful": false, "comment": "The RHEL 10 steps are missing."}
	if pr, err := feedback.Prepare(feedback.Feedback{URL: feedbackPage, Comment: "The RHEL 10 steps are missing."}, "test"); err != nil {
		t.Fatal(err)
	} else {
		approval["confirm_token"] = confirmToken(forApproval, pr.Body(), *now)
	}
	if text, isErr := callRaw(t, cs, "send_feedback", approval, nil); !isErr || len(*got) != 0 {
		t.Errorf("an approval token as a preview token = %q", text)
	}
	// The same fields with the token send it.
	args["confirm_token"] = token
	call(t, cs, "send_feedback", args, &out)
	if out.Status != "sent" || len(*got) != 1 || !strings.Contains((*got)[0], `"helpful":"no"`) {
		t.Errorf("sent = %+v, %v", out, *got)
	}
	// Within the minute, the rate limit stops a second send.
	if text, isErr := callRaw(t, cs, "send_feedback", args, nil); !isErr || !strings.Contains(text, "rate limited") {
		t.Errorf("rate limit = %q", text)
	}
	// Tokens expire.
	*now = now.Add(tokenTTL + time.Minute)
	if text, isErr := callRaw(t, cs, "send_feedback", args, nil); !isErr || !strings.Contains(text, "expired") || len(*got) != 1 {
		t.Errorf("expired = %q", text)
	}
	// Bad input is refused before any preview.
	for _, a := range []map[string]any{{"url": "https://example.com/x", "helpful": true}, {"url": feedbackPage, "helpful": false}} {
		if text, isErr := callRaw(t, cs, "send_feedback", a, nil); !isErr {
			t.Errorf("%v should fail: %s", a, text)
		}
	}
	// Off without a sender (hosted mode).
	if text, isErr := callRaw(t, connect(t, nil), "send_feedback", map[string]any{"url": feedbackPage, "helpful": true}, nil); !isErr || !strings.Contains(text, "isn't available") {
		t.Errorf("off = %q", text)
	}
}

func TestSendFeedbackElicitation(t *testing.T) {
	// The current protocol asks through input requests; older ones, with a request
	// from the server.
	for _, protocol := range []string{"", "2025-11-25"} {
		t.Run("protocol "+protocol, func(t *testing.T) { testElicitation(t, protocol) })
	}
}

func testElicitation(t *testing.T, protocol string) {
	for _, tc := range []struct {
		answer *mcp.ElicitResult
		status string
		sent   int
	}{
		{&mcp.ElicitResult{Action: "accept", Content: map[string]any{"send": true}}, "sent", 1},
		{&mcp.ElicitResult{Action: "accept", Content: map[string]any{"send": false}}, "declined", 0},
		{&mcp.ElicitResult{Action: "decline"}, "declined", 0},
		{&mcp.ElicitResult{Action: "cancel"}, "cancelled", 0},
	} {
		env, got, _ := feedbackEnv(t)
		var asked string
		cs := connectEliciting(t, env, protocol, func(r *mcp.ElicitRequest) *mcp.ElicitResult {
			asked = r.Params.Message
			return tc.answer
		})
		var out feedbackOut
		call(t, cs, "send_feedback", map[string]any{"url": feedbackPage, "helpful": true, "email": "a@example.com"}, &out)
		if out.Status != tc.status || out.Token != "" || len(*got) != tc.sent {
			t.Errorf("%+v: out = %+v, %d sent", tc.answer, out, len(*got))
		}
		if !strings.Contains(asked, "Email: a@example.com") || !strings.Contains(asked, "documentation only") {
			t.Errorf("the user was asked:\n%s", asked)
		}
	}
}
