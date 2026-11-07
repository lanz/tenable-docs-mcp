// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package feedback

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const page = "https://docs.tenable.com/nessus/Content/Install.htm"

func TestPrepare(t *testing.T) {
	p, err := Prepare(Feedback{URL: " " + page + " ", Helpful: false, Comment: " The port list is out of date. ", Email: "a@example.com"}, "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]string
	if err := json.Unmarshal(p.Body(), &body); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"currentUrl": page, "helpful": "no", "comment": "[Tenable Docs MCP 1.0.0] The port list is out of date.", "email": "a@example.com", "source": "tenable-docs-mcp", "type": "feedback"}
	if len(body) != len(want) {
		t.Errorf("body = %v", body)
	}
	for k, v := range want {
		if body[k] != v {
			t.Errorf("%s = %q, want %q", k, body[k], v)
		}
	}
	for _, s := range []string{"Helpful: no", "Comment: [Tenable Docs MCP 1.0.0] The port", "Email: a@example.com", "documentation only"} {
		if !strings.Contains(p.Preview, s) {
			t.Errorf("preview lacks %q:\n%s", s, p.Preview)
		}
	}
	// Helpful, with nothing else: no comment or email in the body.
	p, err = Prepare(Feedback{URL: "https://jp.docs.tenable.com/nessus/Content/Install.htm", Helpful: true}, "1.0.0")
	if err != nil || string(p.Body()) != `{"currentUrl":"https://jp.docs.tenable.com/nessus/Content/Install.htm","helpful":"yes","source":"tenable-docs-mcp","type":"feedback"}` ||
		!strings.Contains(p.Preview, "can't reply") {
		t.Errorf("helpful = %s, %v", p.Body(), err)
	}
	// The limit counts characters, prefix included.
	prefix := len([]rune(Prefix("1.0.0")))
	if _, err := Prepare(Feedback{URL: page, Comment: strings.Repeat("é", MaxComment-prefix)}, "1.0.0"); err != nil {
		t.Errorf("a comment at the limit failed: %v", err)
	}
	for _, f := range []Feedback{
		{URL: "https://example.com/x.htm", Helpful: true},
		{URL: "http://docs.tenable.com/x.htm", Helpful: true},
		{URL: page},
		{URL: page, Comment: "  "},
		{URL: page, Helpful: true, Email: "not an email"},
		{URL: page, Helpful: true, Email: "Name <a@example.com>"},
		{URL: page, Helpful: true, Email: "a@localhost"},
		{URL: page, Comment: strings.Repeat("x", MaxComment-prefix+1)},
	} {
		if _, err := Prepare(f, "1.0.0"); err == nil {
			t.Errorf("%+v should fail", f)
		}
	}
}

func TestPrepareGap(t *testing.T) {
	page := PageDetails{Title: "  Scan Printers  ", Product: "Nessus", GuideType: "User Guide", Language: "en"}
	p, err := Prepare(Feedback{URL: "https://docs.tenable.com/", Gap: true, Helpful: true, Question: " How do I scan printers? ", Comment: "I need the plugin family.", Page: page}, "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]string
	if err := json.Unmarshal(p.Body(), &body); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"currentUrl": "https://docs.tenable.com/", "helpful": "no", "source": "tenable-docs-mcp", "type": "gap",
		"question": "How do I scan printers?", "comment": "[Tenable Docs MCP 1.0.0] Docs gap: How do I scan printers?\n\nI need the plugin family.",
		"title": "Scan Printers", "product": "Nessus", "guideType": "User Guide", "language": "en",
	}
	if len(body) != len(want) {
		t.Errorf("body = %v", body)
	}
	for k, v := range want {
		if body[k] != v {
			t.Errorf("%s = %q, want %q", k, body[k], v)
		}
	}
	if !strings.Contains(p.Preview, "Type: docs gap\nQuestion: How do I scan printers?") || strings.Contains(p.Preview, "Helpful") {
		t.Errorf("preview:\n%s", p.Preview)
	}
	// Without details, the comment is the question.
	p, _ = Prepare(Feedback{URL: page0, Gap: true, Question: "Where are the logs?"}, "1.0.0")
	if !strings.Contains(string(p.Body()), `"comment":"[Tenable Docs MCP 1.0.0] Docs gap: Where are the logs?"`) {
		t.Errorf("body = %s", p.Body())
	}
	// Page details are clipped to the service's limits.
	p, _ = Prepare(Feedback{URL: page0, Helpful: true, Page: PageDetails{Title: strings.Repeat("é", 400), Language: strings.Repeat("x", 30)}}, "1.0.0")
	var clipped map[string]string
	_ = json.Unmarshal(p.Body(), &clipped)
	if len([]rune(clipped["title"])) != maxTitle || len(clipped["language"]) != maxLanguage {
		t.Errorf("clipped = %d, %d", len([]rune(clipped["title"])), len(clipped["language"]))
	}
	for _, f := range []Feedback{
		{URL: page0, Gap: true, Question: "  "},
		{URL: page0, Gap: true, Question: strings.Repeat("x", maxQuestion+1)},
		{URL: page0, Gap: true, Question: "Short?", Comment: strings.Repeat("x", MaxComment)},
	} {
		if _, err := Prepare(f, "1.0.0"); err == nil {
			t.Errorf("%.40v should fail", f)
		}
	}
}

const page0 = "https://docs.tenable.com/nessus/Content/Install.htm"

// fake is a stand-in for the feedback endpoint.
type fake struct {
	*httptest.Server
	bodies  []string
	headers []http.Header
	status  int
}

func newFake(t *testing.T) *fake {
	f := &fake{status: http.StatusOK}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		f.bodies = append(f.bodies, string(b))
		f.headers = append(f.headers, r.Header.Clone())
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.status)
		if f.status != http.StatusOK {
			_, _ = io.WriteString(w, `{"message":"comment is required"}`)
			return
		}
		_, _ = io.WriteString(w, `{}`)
	}))
	t.Cleanup(f.Close)
	return f
}

func TestSend(t *testing.T) {
	f := newFake(t)
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	log := filepath.Join(t.TempDir(), "sub", "feedback.jsonl")
	s := &Sender{Endpoint: f.URL, UserAgent: "tenable-docs-mcp/test", LogFile: log, Now: func() time.Time { return now }}
	p, _ := Prepare(Feedback{URL: page, Helpful: true, Comment: "Clear steps."}, "test")
	ctx := context.Background()
	if err := s.Send(ctx, p); err != nil {
		t.Fatal(err)
	}
	if len(f.bodies) != 1 || f.bodies[0] != string(p.Body()) || f.headers[0].Get("Content-Type") != "application/json" ||
		f.headers[0].Get("User-Agent") != "tenable-docs-mcp/test" {
		t.Errorf("request = %v %v", f.bodies, f.headers)
	}
	data, err := os.ReadFile(log)
	if err != nil || !strings.Contains(string(data), `"currentUrl":"`+page+`"`) || !strings.Contains(string(data), `"time":"2026-11-06T12:00:00Z"`) {
		t.Errorf("log = %s, %v", data, err)
	}

	// One a minute.
	now = now.Add(30 * time.Second)
	if err := s.Send(ctx, p); !errors.Is(err, ErrRateLimited) || len(f.bodies) != 1 {
		t.Errorf("second within a minute = %v", err)
	}
	// Ten a day.
	for range PerDay - 1 {
		now = now.Add(2 * time.Minute)
		if err := s.Send(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	now = now.Add(2 * time.Minute)
	if err := s.Send(ctx, p); !errors.Is(err, ErrRateLimited) || !strings.Contains(err.Error(), "tomorrow") || len(f.bodies) != PerDay {
		t.Errorf("eleventh in a day = %v, %d sent", err, len(f.bodies))
	}
	now = now.Add(24 * time.Hour)
	if err := s.Send(ctx, p); err != nil {
		t.Errorf("the next day = %v", err)
	}

	// A refusal carries the service's message, and isn't recorded.
	f.status = http.StatusBadRequest
	now = now.Add(2 * time.Minute)
	if err := s.Send(ctx, p); err == nil || !strings.Contains(err.Error(), "comment is required") {
		t.Errorf("refused = %v", err)
	}
	now = now.Add(2 * time.Minute)
	f.status = http.StatusOK
	if err := s.Send(ctx, p); err != nil {
		t.Errorf("after a refusal = %v", err)
	}
}

func TestSendNeverReachesTenable(t *testing.T) {
	p, _ := Prepare(Feedback{URL: page, Helpful: true}, "test")
	dir := t.TempDir()
	for _, ep := range []string{Endpoint, "https://WWW.Tenable.com./evaluations/api/v1/docs/feedback", "https://staging.tenable.com/x"} {
		s := &Sender{Endpoint: ep, LogFile: filepath.Join(dir, "f.jsonl"), Client: &http.Client{Transport: failTransport{t}}}
		if err := s.Send(context.Background(), p); err == nil || !strings.Contains(err.Error(), "tests never send") {
			t.Errorf("%s: %v", ep, err)
		}
	}
	if err := (&Sender{}).Send(context.Background(), p); err == nil {
		t.Error("an unset sender should fail")
	}
}

type failTransport struct{ t *testing.T }

func (f failTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	f.t.Errorf("request to %s", r.URL)
	return nil, errors.New("no requests in this test")
}

func TestDefaultLogFile(t *testing.T) {
	p, err := DefaultLogFile()
	if err == nil && filepath.Base(p) != "feedback.jsonl" {
		t.Errorf("path = %s", p)
	}
}
