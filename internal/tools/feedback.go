// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lanz/tenable-docs-mcp/internal/feedback"
)

// The agent chooses only the page and whether it helped. The user writes the comment
// and email in a form their client shows, so the agent can't put words in their mouth.
type feedbackIn struct {
	URL     string `json:"url" jsonschema:"the docs page the feedback is about"`
	Helpful bool   `json:"helpful" jsonschema:"whether the user found the page helpful, from what they said"`
}

type feedbackOut struct {
	Status string `json:"status" jsonschema:"sent, declined, cancelled, or no_form (this client can't show the feedback form; nothing was sent)"`
	// Sent is exactly what was sent, when it was.
	Sent string `json:"sent,omitempty" jsonschema:"exactly what was sent"`
	// Page is where the user can use the docs site's own feedback form instead.
	Page string `json:"page"`
}

// formID names the form that asks the user for their feedback.
const formID = "feedback"

// formTTL is how long the user has to fill in the form.
const formTTL = 30 * time.Minute

// formKey signs form state; it lasts as long as the process.
var formKey = func() []byte {
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		panic(err)
	}
	return k
}()

// formState binds a form to the page, the helpful answer, and a time, so the answer
// comes back to the feedback it was asked for.
func formState(page string, helpful bool, at time.Time) string {
	var ts [8]byte
	binary.BigEndian.PutUint64(ts[:], uint64(at.Unix()))
	m := hmac.New(sha256.New, formKey)
	m.Write(ts[:])
	fmt.Fprintf(m, "%t\x00%s", helpful, page)
	return base64.RawURLEncoding.EncodeToString(append(ts[:], m.Sum(nil)[:16]...))
}

func checkFormState(state, page string, helpful bool, now time.Time) error {
	raw, err := base64.RawURLEncoding.DecodeString(state)
	if err != nil || len(raw) != 24 {
		return errors.New("that form answer isn't for a form this server showed; nothing was sent")
	}
	at := time.Unix(int64(binary.BigEndian.Uint64(raw[:8])), 0)
	if !hmac.Equal([]byte(formState(page, helpful, at)), []byte(state)) {
		return errors.New("that form answer is for other feedback; nothing was sent")
	}
	if now.Sub(at) > formTTL || at.After(now.Add(time.Minute)) {
		return errors.New("the feedback form expired; nothing was sent")
	}
	return nil
}

func registerFeedback(s *mcp.Server, env *Env) {
	notDestructive, open := false, true
	mcp.AddTool(s, &mcp.Tool{
		Name: "send_feedback",
		Description: "Open a form in which the user writes feedback about a docs page for Tenable's documentation " +
			"team, like the feedback form on each docs page. Use it when the user wants to give feedback on a " +
			"page. Choose the page and whether it was helpful from what the user said; the user writes the " +
			"comment, and their email if they want a reply, in the form. You can't write feedback for them. " +
			"Feedback is for the documentation, not for Tenable products. If the user's client can't show " +
			"forms, nothing is sent; give them the page's link to use its own feedback form.",
		Annotations: &mcp.ToolAnnotations{Title: "Send docs feedback", DestructiveHint: &notDestructive, OpenWorldHint: &open},
	}, func(ctx context.Context, req *mcp.CallToolRequest, in feedbackIn) (*mcp.CallToolResult, feedbackOut, error) {
		if env.Feedback == nil {
			return nil, feedbackOut{}, errors.New("sending feedback isn't available in this mode; use the feedback form at the bottom of the docs page")
		}
		u, err := normalizeURL(in.URL)
		if err != nil {
			return nil, feedbackOut{}, err
		}
		// Check the page now, before showing the user a form.
		if _, err := feedback.Prepare(feedback.Feedback{URL: u, Helpful: true}, env.Info.Version); err != nil {
			return nil, feedbackOut{}, err
		}
		out := feedbackOut{Page: u}
		now := env.now()
		switch {
		case req.Params.InputResponses != nil:
			return env.sendForm(ctx, req, in, out, now)
		case canElicit(req):
			// Show the form; the SDK calls this tool again with what the user entered.
			return &mcp.CallToolResult{
				InputRequests: mcp.InputRequestMap{formID: feedbackForm(u, in.Helpful, env.Info.Version)},
				RequestState:  formState(u, in.Helpful, now),
			}, feedbackOut{}, nil
		default:
			out.Status = "no_form"
			return summary("Nothing was sent. This client can't show the form in which the user writes their feedback, "+
				"so give them the page's link: the feedback form at the bottom of the page sends it to Tenable's "+
				"documentation team. %s", u), out, nil
		}
	})
}

// feedbackForm asks the user for their comment and, optionally, their email. Both start
// empty. The comment is required only when the page wasn't helpful, as on the docs
// site, and accepting the form is the approval.
func feedbackForm(page string, helpful bool, version string) *mcp.ElicitParams {
	answer, need := "yes", "optional"
	if !helpful {
		answer, need = "no", "required"
	}
	limit := feedback.MaxComment - utf8.RuneCountInString(feedback.Prefix(version))
	comment := map[string]any{
		"type": "string", "title": "Comment", "maxLength": limit,
		"description": "What was helpful, wrong, or missing (" + need + ")",
	}
	if !helpful {
		comment["minLength"] = 1
	}
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"comment": comment,
			"email":   map[string]any{"type": "string", "title": "Email (optional)", "description": "Only if you want the docs team to reply"},
		},
	}
	if !helpful {
		schema["required"] = []string{"comment"}
	}
	return &mcp.ElicitParams{
		Message: "Feedback for Tenable's documentation team\n\nPage: " + page + "\nHelpful: " + answer +
			"\n\nWrite your comment (" + need + "), and add your email only if you want a reply. " +
			"Accept sends what you enter, as you typed it.\n" + feedback.Note,
		RequestedSchema: schema,
	}
}

// sendForm sends what the user entered in the form.
func (e *Env) sendForm(ctx context.Context, req *mcp.CallToolRequest, in feedbackIn, out feedbackOut, now time.Time) (*mcp.CallToolResult, feedbackOut, error) {
	if err := checkFormState(req.Params.RequestState, out.Page, in.Helpful, now); err != nil {
		return nil, feedbackOut{}, err
	}
	res, _ := req.Params.InputResponses[formID].(*mcp.ElicitResult)
	if res == nil || res.Action == "cancel" {
		out.Status = "cancelled"
		return summary("The user cancelled; nothing was sent."), out, nil
	}
	if res.Action != "accept" {
		out.Status = "declined"
		return summary("The user declined; nothing was sent."), out, nil
	}
	comment, _ := res.Content["comment"].(string)
	email, _ := res.Content["email"].(string)
	p, err := feedback.Prepare(feedback.Feedback{URL: out.Page, Helpful: in.Helpful, Comment: comment, Email: email}, e.Info.Version)
	if err != nil {
		return nil, feedbackOut{}, fmt.Errorf("not sent: %w", err)
	}
	if err := e.Feedback.Send(ctx, p); err != nil {
		if errors.Is(err, feedback.ErrRateLimited) {
			return nil, feedbackOut{}, fmt.Errorf("not sent: %w", err)
		}
		return nil, feedbackOut{}, err
	}
	out.Status, out.Sent = "sent", p.Preview
	return summary("Sent the user's feedback to Tenable's documentation team:\n\n%s", strings.TrimSpace(p.Preview)), out, nil
}

// canElicit reports whether the client can show the user a form.
func canElicit(req *mcp.CallToolRequest) bool {
	if req == nil || req.Session == nil {
		return false
	}
	p := req.Session.InitializeParams()
	if p == nil || p.Capabilities == nil || p.Capabilities.Elicitation == nil {
		return false
	}
	e := p.Capabilities.Elicitation
	return e.Form != nil || e.URL == nil
}

func (e *Env) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}
