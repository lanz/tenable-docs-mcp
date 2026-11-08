// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package published

import (
	"context"
	"errors"
	"sync"
	"time"
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
	w.mu.Lock()
	w.updating, w.step = true, "Checking for an updated index"
	w.mu.Unlock()
	prev := u.Progress
	u.Progress = func(msg string) {
		w.mu.Lock()
		w.step = msg
		w.mu.Unlock()
		w.log(msg)
		if prev != nil {
			prev(msg)
		}
	}
	res, err := u.Update(ctx)
	u.Progress = prev
	w.mu.Lock()
	w.updating, w.step = false, ""
	w.mu.Unlock()
	switch {
	case errors.Is(err, ErrBusy):
		// The other process's build is picked up on a later tick.
	case err != nil:
		if ctx.Err() == nil {
			w.log("updating the index: " + err.Error())
		}
	case len(res.Imported) > 0:
		w.log("updated the index from the snapshot built " + res.BuiltAt)
		w.changed()
	}
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
