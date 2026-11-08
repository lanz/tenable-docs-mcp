// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package published

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/lanz/tenable-docs-mcp/internal/crawl"
)

// How often the watcher does what it does.
const (
	// CheckEvery is how often the snapshot is checked for an update.
	CheckEvery = 24 * time.Hour
	// RetryAfter is how long after a failed check the next one waits, and
	// RetryEmptyAfter the same for an index that has nothing in it yet.
	RetryAfter      = time.Hour
	RetryEmptyAfter = 5 * time.Minute
	// tick is how often the watcher looks for builds another process installed, and
	// whether a check is due.
	tick = time.Minute
)

// Watcher keeps an index up to date in the background, while the server runs.
type Watcher struct {
	Updater *Updater
	// Log, when set, reports what the watcher does (the server writes it to stderr).
	Log func(msg string)
	// OnChange, when set, is called after the index changes: an update rebuilt
	// shards, or the index switched to builds another process installed.
	OnChange func()
	// Crawler, when set, keeps the index up to date from the live sites while the
	// published snapshot can't be reached; nil turns that fallback off.
	Crawler *crawl.Crawler

	mu       sync.Mutex
	updating bool
	step     string
}

// Status is what the watcher is doing, and the last check's state.
type Status struct {
	State
	// Updating is set while an update runs, and Step says what it's doing.
	Updating bool   `json:"updating"`
	Step     string `json:"step,omitempty"`
}

// Status returns the watcher's status.
func (w *Watcher) Status() Status {
	st, _ := ReadState(w.Updater.Index.Dir)
	w.mu.Lock()
	defer w.mu.Unlock()
	return Status{State: st, Updating: w.updating, Step: w.step}
}

func (w *Watcher) log(msg string) {
	if w.Log != nil {
		w.Log(msg)
	}
}

// Run watches until ctx ends.
func (w *Watcher) Run(ctx context.Context) {
	for {
		w.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(tick):
		}
	}
}

// Tick switches to builds another process installed, and updates the index when a
// check is due.
func (w *Watcher) Tick(ctx context.Context) {
	u := w.Updater
	if changed, err := u.Index.Reload(); err != nil {
		w.log("reloading the index: " + err.Error())
	} else if changed {
		w.log("switched to the index another process updated")
		w.changed()
	}
	st, _ := ReadState(u.Index.Dir)
	if !Due(st, len(u.Index.Keys()) == 0, u.now()) {
		return
	}
	w.begin("Checking for an updated index")
	prev := u.Progress
	u.Progress = func(msg string) {
		w.setStep(msg)
		if prev != nil {
			prev(msg)
		}
	}
	res, err := u.Update(ctx)
	u.Progress = prev
	w.end()
	switch {
	case errors.Is(err, ErrBusy):
		// The other process's build is picked up on a later tick.
	case err != nil:
		if ctx.Err() == nil {
			w.log("updating the index: " + err.Error())
			w.fallBack(ctx)
		}
	case len(res.Imported) > 0:
		w.log("updated the index from the snapshot built " + res.BuiltAt)
		w.changed()
	}
}

func (w *Watcher) begin(step string) {
	w.mu.Lock()
	w.updating, w.step = true, step
	w.mu.Unlock()
}

func (w *Watcher) setStep(step string) {
	w.mu.Lock()
	w.step = step
	w.mu.Unlock()
	w.log(step)
}

func (w *Watcher) end() {
	w.mu.Lock()
	w.updating, w.step = false, ""
	w.mu.Unlock()
}

// fallBack crawls the live sites, after the published snapshot couldn't be reached,
// when a crawl is due.
func (w *Watcher) fallBack(ctx context.Context) {
	c, u := w.Crawler, w.Updater
	if c == nil {
		return
	}
	st, _ := ReadState(u.Index.Dir)
	if !CrawlDue(st, len(u.Index.Keys()) == 0, u.now()) {
		return
	}
	l, err := Lock(u.Index.Dir, u.now())
	if err != nil {
		return // another process is updating
	}
	defer l.Release()
	w.begin("Reading the live docs sites instead")
	prev := c.Progress
	c.Progress = func(msg string) {
		l.Touch()
		w.setStep(msg)
		if prev != nil {
			prev(msg)
		}
	}
	rep, err := c.Sync(ctx)
	c.Progress = prev
	w.end()
	if ctx.Err() != nil {
		return
	}
	st, _ = ReadState(u.Index.Dir)
	at := u.now().UTC().Format(time.RFC3339)
	if err != nil {
		st.CrawlError, st.CrawlErrorAt = err.Error(), at
		w.log("reading the live sites: " + err.Error())
	} else {
		st.CrawledAt, st.CrawlError, st.CrawlErrorAt, st.Source = at, "", "", SourceLive
		w.log(fmt.Sprintf("updated the index from the live sites: %d pages added, %d updated, %d removed; %d failed",
			rep.Added, rep.Updated, rep.Removed, len(rep.Failed)))
	}
	_ = WriteState(u.Index.Dir, st)
	if rep.Changed() > 0 || len(rep.Built) > 0 {
		w.changed()
	}
}

// CrawlDue reports whether a crawl is due, once the published snapshot couldn't be
// reached: CheckEvery after the last one finished, and RetryAfter after one failed.
// An empty index is crawled right away.
func CrawlDue(st State, empty bool, now time.Time) bool {
	if st.CrawlErrorAt != "" {
		if t, err := time.Parse(time.RFC3339, st.CrawlErrorAt); err == nil && now.Sub(t) < RetryAfter {
			return false
		}
	}
	if empty || st.CrawledAt == "" {
		return true
	}
	t, err := time.Parse(time.RFC3339, st.CrawledAt)
	return err != nil || now.Sub(t) >= CheckEvery
}

func (w *Watcher) changed() {
	if w.OnChange != nil {
		w.OnChange()
	}
}

// Due reports whether a check is due: CheckEvery after the last one succeeded, and
// RetryAfter (or RetryEmptyAfter, for an empty index) after one failed.
func Due(st State, empty bool, now time.Time) bool {
	if st.ErrorAt != "" {
		if t, err := time.Parse(time.RFC3339, st.ErrorAt); err == nil {
			wait := RetryAfter
			if empty {
				wait = RetryEmptyAfter
			}
			if now.Sub(t) < wait {
				return false
			}
		}
	}
	if empty || st.CheckedAt == "" {
		return true
	}
	t, err := time.Parse(time.RFC3339, st.CheckedAt)
	return err != nil || now.Sub(t) >= CheckEvery
}
