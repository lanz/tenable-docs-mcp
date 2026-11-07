// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

// Package feedback sends page feedback to Tenable's documentation team, through the
// endpoint the feedback widget on every docs page uses, so it reaches the team the same
// way. It only sends what the user has approved; callers show the Preview first.
package feedback

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/lanz/tenable-docs-mcp/internal/site"
)

// Endpoint is the docs feedback widget's endpoint.
const Endpoint = "https://www.tenable.com/evaluations/api/v1/docs/feedback"

// MaxComment is the endpoint's limit on a comment, prefix included.
const MaxComment = 1000

// Rate limits on sending, across every session on this computer.
const (
	PerMinute = 1
	PerDay    = 10
)

// Note ends every preview, as the feedback widget says it.
const Note = "Note that feedback is for documentation only and not Tenable products."

// Source identifies feedback sent through this server, in the request's source field.
const Source = "tenable-docs-mcp"

// Feedback is what the user wants to send about a page.
type Feedback struct {
	URL     string
	Helpful bool
	Comment string
	// Email is optional, for the docs team to reply.
	Email string
}

// payload is the request body, as the widget sends it.
type payload struct {
	CurrentURL string `json:"currentUrl"`
	Helpful    string `json:"helpful"`
	Comment    string `json:"comment,omitempty"`
	Email      string `json:"email,omitempty"`
	Source     string `json:"source"`
}

// Prefix starts every comment, so the docs team can tell feedback sent through this
// server from the widget's.
func Prefix(version string) string {
	return "[Tenable Docs MCP " + version + "] "
}

// Prepared is feedback checked and ready to send, exactly as it will be sent.
type Prepared struct {
	body    payload
	Preview string
}

// Body returns the request body, exactly as it will be sent.
func (p *Prepared) Body() []byte {
	b, _ := json.Marshal(p.body)
	return b
}

// Prepare checks feedback and builds the request it will send.
func Prepare(f Feedback, version string) (*Prepared, error) {
	u, err := url.Parse(strings.TrimSpace(f.URL))
	if err != nil || u.Scheme != "https" || !site.IsDocsHost(u.Host) {
		return nil, fmt.Errorf("%q isn't a page on docs.tenable.com or one of its translated sites", f.URL)
	}
	comment := strings.TrimSpace(f.Comment)
	if !f.Helpful && comment == "" {
		return nil, errors.New("say what's wrong or missing: a comment is required when the page wasn't helpful")
	}
	email := strings.TrimSpace(f.Email)
	if email != "" {
		if a, err := mail.ParseAddress(email); err != nil || a.Address != email || !strings.Contains(email[strings.IndexByte(email, '@'):], ".") {
			return nil, fmt.Errorf("%q isn't an email address", email)
		}
	}
	if comment != "" {
		comment = Prefix(version) + comment
	}
	if n := utf8.RuneCountInString(comment); n > MaxComment {
		return nil, fmt.Errorf("the comment is %d characters with its %d-character prefix; the limit is %d", n, utf8.RuneCountInString(Prefix(version)), MaxComment)
	}
	p := &Prepared{body: payload{CurrentURL: u.String(), Helpful: "no", Comment: comment, Email: email, Source: Source}}
	if f.Helpful {
		p.body.Helpful = "yes"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Page: %s\nHelpful: %s\n", p.body.CurrentURL, p.body.Helpful)
	if comment != "" {
		fmt.Fprintf(&b, "Comment: %s\n", comment)
	}
	if email != "" {
		fmt.Fprintf(&b, "Email: %s\n", email)
	} else {
		b.WriteString("Email: none (the docs team can't reply)\n")
	}
	b.WriteString(Note)
	p.Preview = b.String()
	return p, nil
}

// Sender sends prepared feedback, within the rate limits.
type Sender struct {
	// Endpoint is where feedback goes; tests point it at a fake.
	Endpoint  string
	Client    *http.Client
	UserAgent string
	// LogFile records each sent item and is where the rate limits are counted; it's
	// readable only by its owner.
	LogFile string
	Now     func() time.Time
}

// ErrRateLimited is returned when sending now would pass a rate limit.
var ErrRateLimited = errors.New("rate limited")

var mu sync.Mutex

// sent is one line of the log.
type sent struct {
	Time time.Time `json:"time"`
	payload
}

// Send sends prepared feedback, and records it.
func (s *Sender) Send(ctx context.Context, p *Prepared) error {
	if s.Endpoint == "" || s.LogFile == "" {
		return errors.New("the feedback sender isn't set up")
	}
	if u, err := url.Parse(s.Endpoint); err != nil || testing.Testing() && isTenable(u.Hostname()) {
		return errors.New("tests never send to Tenable")
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	mu.Lock()
	defer mu.Unlock()
	t := now().UTC()
	if wait, err := s.check(t); err != nil {
		return err
	} else if wait != "" {
		return fmt.Errorf("%w: %s", ErrRateLimited, wait)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.Endpoint, bytes.NewReader(p.Body()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", s.UserAgent)
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send feedback: %w", err)
	}
	defer resp.Body.Close()
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(msg, &e)
		if e.Message == "" {
			e.Message = resp.Status
		}
		return fmt.Errorf("the feedback service refused the feedback: %s", e.Message)
	}
	return s.record(sent{Time: t, payload: p.body})
}

func isTenable(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	return host == "tenable.com" || strings.HasSuffix(host, ".tenable.com")
}

// check returns why sending at t would pass a rate limit, or "".
func (s *Sender) check(t time.Time) (string, error) {
	log, err := s.read()
	if err != nil {
		return "", err
	}
	minute, day := 0, 0
	for _, e := range log {
		if t.Sub(e.Time) < time.Minute {
			minute++
		}
		if t.Sub(e.Time) < 24*time.Hour {
			day++
		}
	}
	switch {
	case minute >= PerMinute:
		return "one feedback item a minute; try again shortly", nil
	case day >= PerDay:
		return fmt.Sprintf("at most %d feedback items a day; try again tomorrow", PerDay), nil
	}
	return "", nil
}

func (s *Sender) read() ([]sent, error) {
	data, err := os.ReadFile(s.LogFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	var out []sent
	for _, line := range bytes.Split(data, []byte("\n")) {
		var e sent
		if json.Unmarshal(line, &e) == nil && !e.Time.IsZero() {
			out = append(out, e)
		}
	}
	return out, nil
}

func (s *Sender) record(e sent) error {
	if err := os.MkdirAll(filepath.Dir(s.LogFile), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(s.LogFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	line, _ := json.Marshal(e)
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// DefaultLogFile returns where sent feedback is recorded: tenable-docs-mcp/feedback.jsonl
// in the user's settings folder.
func DefaultLogFile() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "tenable-docs-mcp", "feedback.jsonl"), nil
}
