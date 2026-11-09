// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lanz/tenable-docs-mcp/internal/feedback"
	"github.com/lanz/tenable-docs-mcp/internal/gaps"
)

// answerAgain calls a tool with a form answer and state, as a client does after
// showing the form, and as a client that retries, or anything replaying it, would.
func answerAgain(t *testing.T, cs *mcp.ClientSession, tool string, args map[string]any, form, state string, answer *mcp.ElicitResult) (string, bool) {
	t.Helper()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args,
		InputResponses: mcp.InputResponseMap{form: answer}, RequestState: state})
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	var text []string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text = append(text, tc.Text)
		}
	}
	return strings.Join(text, "\n"), res.IsError
}

// A form's answer is sent once, even when it comes back again after the one-a-minute
// limit has passed.
func TestFormAnswersAreSentOnce(t *testing.T) {
	env, got, now := feedbackEnv(t)
	cs := connectEnv(t, env)
	args := map[string]any{"url": feedbackPage, "helpful": false}
	state := formState(feedbackPage, false, *now)
	answer := &mcp.ElicitResult{Action: "accept", Content: map[string]any{"comment": "Step 3 is missing."}}
	if text, isErr := answerAgain(t, cs, "open_feedback_form", args, formID, state, answer); isErr || len(*got) != 1 {
		t.Fatalf("first: %v %q, sent %d", isErr, text, len(*got))
	}
	*now = now.Add(2 * time.Minute)
	if text, isErr := answerAgain(t, cs, "open_feedback_form", args, formID, state, answer); !isErr || !strings.Contains(text, "already sent") {
		t.Errorf("replayed: %v %q", isErr, text)
	}
	if len(*got) != 1 {
		t.Errorf("sent %d times", len(*got))
	}
	// A new form for the same page is a new answer.
	if _, isErr := answerAgain(t, cs, "open_feedback_form", args, formID, formState(feedbackPage, false, *now), answer); isErr || len(*got) != 2 {
		t.Errorf("a second form: %v, sent %d", isErr, len(*got))
	}
}

// When sending fails, the form's answer can be sent again.
func TestFailedSendReleasesTheForm(t *testing.T) {
	env, got, now := feedbackEnv(t)
	cs := connectEnv(t, env)
	args := map[string]any{"url": feedbackPage, "helpful": false}
	answer := &mcp.ElicitResult{Action: "accept", Content: map[string]any{"comment": "Step 3 is missing."}}
	// Something else was sent a moment ago, so this one is over the limit.
	if _, isErr := answerAgain(t, cs, "open_feedback_form", args, formID, formState(feedbackPage, false, *now), answer); isErr {
		t.Fatal("first send failed")
	}
	state := formState(feedbackPage, false, *now)
	if text, isErr := answerAgain(t, cs, "open_feedback_form", args, formID, state, answer); !isErr || !strings.Contains(text, "not sent") {
		t.Fatalf("over the limit: %v %q", isErr, text)
	}
	*now = now.Add(2 * time.Minute)
	if text, isErr := answerAgain(t, cs, "open_feedback_form", args, formID, state, answer); isErr || len(*got) != 2 {
		t.Errorf("after the limit: %v %q, sent %d", isErr, text, len(*got))
	}
}

// A gap form's state covers its question, which is sent when the form's is left empty.
func TestGapStateCoversTheQuestion(t *testing.T) {
	env, got, now := gapEnv(t)
	cs := connectEnv(t, env)
	question := "How do I scan printers?"
	state := gapState(feedbackPage, question, *now)
	empty := &mcp.ElicitResult{Action: "accept"}
	args := map[string]any{"question": "Something else entirely", "url": feedbackPage}
	if text, isErr := answerAgain(t, cs, "report_gap", args, gapFormID, state, empty); !isErr || !strings.Contains(text, "other feedback") {
		t.Errorf("another question: %v %q", isErr, text)
	}
	args["question"] = "  " + question + " "
	if text, isErr := answerAgain(t, cs, "report_gap", args, gapFormID, state, empty); isErr || len(*got) != 1 || !strings.Contains((*got)[0], question) {
		t.Errorf("its question: %v %q, sent %v", isErr, text, *got)
	}
}

// The gap form's fields together fit in one comment, and a long recorded question is
// clipped to fit its field.
func TestGapFormLimits(t *testing.T) {
	for _, v := range []string{"test", "v1.0.0-rc.1", "v0.0.0-20261109120000-0123456789ab+dirty"} {
		q, d := gapLimits(v)
		long := strings.Repeat("q", q)
		details := strings.Repeat("d", d)
		_, err := feedback.Prepare(feedback.Feedback{URL: feedbackPage, Gap: true, Question: long, Comment: details}, v)
		if err != nil || q > gaps.MaxQuestion || q < 400 {
			t.Errorf("%s: limits %d and %d: %v", v, q, d, err)
		}
	}
	env, got, _ := gapEnv(t)
	var asked *mcp.ElicitParams
	cs := connectEliciting(t, env, "", func(r *mcp.ElicitRequest) *mcp.ElicitResult {
		asked = r.Params
		return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"details": strings.Repeat("d", 300)}}
	})
	question := strings.Repeat("Why does the scanner stop? ", 30) // 810 characters
	if text, isErr := callRaw(t, cs, "report_gap", map[string]any{"question": question}, nil); isErr || len(*got) != 1 {
		t.Fatalf("%v %q, sent %d", isErr, text, len(*got))
	}
	var schema struct {
		Properties map[string]struct {
			Default   string `json:"default"`
			MaxLength int    `json:"maxLength"`
		} `json:"properties"`
	}
	b, _ := json.Marshal(asked.RequestedSchema)
	_ = json.Unmarshal(b, &schema)
	q := schema.Properties["question"]
	if n := utf8.RuneCountInString(q.Default); n == 0 || n > q.MaxLength {
		t.Errorf("the default is %d characters; the field takes %d", n, q.MaxLength)
	}
	// The whole question is still recorded.
	if recs := recorded(t, env); len(recs) != 1 || recs[0].Question != strings.TrimSpace(question) {
		t.Errorf("recorded %d", len(recs))
	}
}
