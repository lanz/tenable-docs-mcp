// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package feedback

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Another process's send holds the log's lock, so this one waits, and gives up
// without sending if it isn't released; a lock left by a process that stopped is
// taken over.
func TestSendWaitsForAnotherProcess(t *testing.T) {
	oldWait := lockWait
	t.Cleanup(func() { lockWait = oldWait })
	lockWait = 200 * time.Millisecond
	f := newFake(t)
	log := filepath.Join(t.TempDir(), "feedback.jsonl")
	s := &Sender{Endpoint: f.URL, LogFile: log}
	p, _ := Prepare(Feedback{URL: page, Helpful: true}, "test")
	if err := os.WriteFile(log+".lock", []byte("1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Send(context.Background(), p); !errors.Is(err, ErrRateLimited) || !strings.Contains(err.Error(), "another") || len(f.bodies) != 0 {
		t.Errorf("held = %v, %d sent", err, len(f.bodies))
	}
	if err := s.Check(); !errors.Is(err, ErrRateLimited) {
		t.Errorf("Check while held = %v", err)
	}
	old := time.Now().Add(-2 * lockStale)
	if err := os.Chtimes(log+".lock", old, old); err != nil {
		t.Fatal(err)
	}
	if err := s.Send(context.Background(), p); err != nil || len(f.bodies) != 1 {
		t.Errorf("stale = %v, %d sent", err, len(f.bodies))
	}
	if _, err := os.Stat(log + ".lock"); !os.IsNotExist(err) {
		t.Errorf("the lock is left: %v", err)
	}
}

// An entry dated after now, from before the clock went back, doesn't hold up sending.
func TestFutureEntriesDontCount(t *testing.T) {
	f := newFake(t)
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	log := filepath.Join(t.TempDir(), "feedback.jsonl")
	if err := os.WriteFile(log, []byte(`{"time":"2026-11-06T12:30:00Z","currentUrl":"`+page+`"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &Sender{Endpoint: f.URL, LogFile: log, Now: func() time.Time { return now }}
	if err := s.Check(); err != nil {
		t.Errorf("Check = %v", err)
	}
}

// A request that fails partway may have reached the service, so it counts toward the
// limits, marked unconfirmed.
func TestUnconfirmedSendCounts(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	log := filepath.Join(t.TempDir(), "feedback.jsonl")
	s := &Sender{Endpoint: "https://feedback.example/x", LogFile: log, Now: func() time.Time { return now },
		Client: &http.Client{Transport: brokenTransport{}}}
	p, _ := Prepare(Feedback{URL: page, Helpful: true}, "test")
	if err := s.Send(context.Background(), p); err == nil {
		t.Fatal("no error")
	}
	if data, _ := os.ReadFile(log); !strings.Contains(string(data), `"unconfirmed":true`) {
		t.Errorf("log = %s", data)
	}
	if err := s.Check(); !errors.Is(err, ErrRateLimited) {
		t.Errorf("Check = %v", err)
	}
}

type brokenTransport struct{}

func (brokenTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("connection reset")
}
