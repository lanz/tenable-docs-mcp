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
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lanz/tenable-docs-mcp/internal/feedback"
)

type feedbackIn struct {
	URL     string `json:"url" jsonschema:"the docs page the feedback is about"`
	Helpful bool   `json:"helpful" jsonschema:"whether the page was helpful"`
	Comment string `json:"comment,omitempty" jsonschema:"what was helpful, wrong, or missing, in the user's words; required when the page wasn't helpful; at most about 970 characters"`
	Email   string `json:"email,omitempty" jsonschema:"the user's email, only if they want the docs team to reply"`
	Confirm string `json:"confirm_token,omitempty" jsonschema:"the token from the preview, after the user approved it; the other fields must be unchanged"`
}

type feedbackOut struct {
	// Status is preview, sent, declined, or cancelled.
	Status  string `json:"status" jsonschema:"preview (shown, not sent), sent, declined, or cancelled"`
	Preview string `json:"preview" jsonschema:"exactly what is or would be sent"`
	Token   string `json:"confirm_token,omitempty" jsonschema:"for a preview: pass it back, with the same fields, once the user approves"`
}

// approvalID names the request that asks the user to approve feedback.
const approvalID = "approve"

// tokenTTL is how long a preview's token works.
const tokenTTL = 15 * time.Minute

// confirmKey signs preview tokens; it lasts as long as the process.
var confirmKey = func() []byte {
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		panic(err)
	}
	return k
}()

// Token purposes: a preview the model passes back, or an approval request the client
// answers. Neither works as the other.
const (
	forPreview  = "preview"
	forApproval = "approval"
)

// confirmToken binds a token to its purpose, the exact request body, and a time.
func confirmToken(purpose string, body []byte, at time.Time) string {
	var ts [8]byte
	binary.BigEndian.PutUint64(ts[:], uint64(at.Unix()))
	m := hmac.New(sha256.New, confirmKey)
	m.Write([]byte(purpose))
	m.Write([]byte{0})
	m.Write(ts[:])
	m.Write(body)
	return base64.RawURLEncoding.EncodeToString(append(ts[:], m.Sum(nil)[:16]...))
}

func checkToken(purpose, token string, body []byte, now time.Time) error {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 24 {
		return errors.New("that confirm_token isn't one this server gave; preview the feedback again")
	}
	at := time.Unix(int64(binary.BigEndian.Uint64(raw[:8])), 0)
	if !hmac.Equal([]byte(confirmToken(purpose, body, at)), []byte(token)) {
		return errors.New("the feedback changed since the user approved the preview, or the token is from another session; preview it again and ask the user")
	}
	if now.Sub(at) > tokenTTL || at.After(now.Add(time.Minute)) {
		return errors.New("the preview's token has expired; preview the feedback again and ask the user")
	}
	return nil
}

func registerFeedback(s *mcp.Server, env *Env) {
	notDestructive, open := false, true
	mcp.AddTool(s, &mcp.Tool{
		Name: "send_feedback",
		Description: "Send feedback about a docs page to Tenable's documentation team, as the page's feedback " +
			"widget does: whether it helped, and what was wrong or missing. Only when the user asks to send " +
			"feedback, with the comment in their words; feedback is for the documentation, not for Tenable " +
			"products. The user always sees exactly what will be sent first: either their client asks them, or " +
			"this tool returns a preview and a confirm_token, and nothing is sent until you call it again with " +
			"the token after the user approves. Never approve for them. Leave out customer details and secrets.",
		Annotations: &mcp.ToolAnnotations{Title: "Send docs feedback", DestructiveHint: &notDestructive, OpenWorldHint: &open},
	}, func(ctx context.Context, req *mcp.CallToolRequest, in feedbackIn) (*mcp.CallToolResult, feedbackOut, error) {
		if env.Feedback == nil {
			return nil, feedbackOut{}, errors.New("sending feedback isn't available in this mode; use the feedback form at the bottom of the docs page")
		}
		u, err := normalizeURL(in.URL)
		if err != nil {
			return nil, feedbackOut{}, err
		}
		p, err := feedback.Prepare(feedback.Feedback{URL: u, Helpful: in.Helpful, Comment: in.Comment, Email: in.Email}, env.Info.Version)
		if err != nil {
			return nil, feedbackOut{}, err
		}
		out := feedbackOut{Preview: p.Preview}
		now := env.now()
		switch {
		case in.Confirm != "":
			if err := checkToken(forPreview, in.Confirm, p.Body(), now); err != nil {
				return nil, feedbackOut{}, err
			}
		case req.Params.InputResponses != nil:
			// The user's answer to the approval request below. The request state binds
			// it to this exact feedback.
			if err := checkToken(forApproval, req.Params.RequestState, p.Body(), now); err != nil {
				return nil, feedbackOut{}, err
			}
			res, _ := req.Params.InputResponses[approvalID].(*mcp.ElicitResult)
			if res == nil || res.Action == "cancel" {
				out.Status = "cancelled"
				return summary("The user cancelled; nothing was sent."), out, nil
			}
			if res.Action != "accept" {
				out.Status = "declined"
				return summary("The user declined; nothing was sent."), out, nil
			}
		case canElicit(req):
			// Ask the user through the client; the SDK retries this call with the answer.
			return &mcp.CallToolResult{
				InputRequests: mcp.InputRequestMap{approvalID: &mcp.ElicitParams{
					Message: "Send this feedback to Tenable's documentation team? Accept to send it.\n\n" + p.Preview,
					// No fields: accepting is the approval. A checkbox only repeated it,
					// and some clients won't accept until it's ticked.
					RequestedSchema: map[string]any{"type": "object", "properties": map[string]any{}},
				}},
				RequestState: confirmToken(forApproval, p.Body(), now),
			}, feedbackOut{}, nil
		default:
			out.Status, out.Token = "preview", confirmToken(forPreview, p.Body(), now)
			return summary("Not sent yet. Show the user this preview, exactly, and ask whether to send it:\n\n%s\n\n"+
				"If they approve, call send_feedback again with the same fields and confirm_token %q. "+
				"If they want changes, make them and preview again.", p.Preview, out.Token), out, nil
		}
		if err := env.Feedback.Send(ctx, p); err != nil {
			if errors.Is(err, feedback.ErrRateLimited) {
				return nil, feedbackOut{}, fmt.Errorf("not sent: %w", err)
			}
			return nil, feedbackOut{}, err
		}
		out.Status = "sent"
		return summary("Sent to Tenable's documentation team:\n\n%s", p.Preview), out, nil
	})
}

// canElicit reports whether the client can ask the user to approve feedback itself.
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
