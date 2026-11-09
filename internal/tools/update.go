// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lanz/tenable-docs-mcp/internal/published"
	"github.com/lanz/tenable-docs-mcp/internal/version"
)

// UpdateAvailable reports whether a newer release of the server is available. The
// published snapshot names the newest release, and the last update check recorded it
// in the index folder, so this makes no request.
func (e *Env) UpdateAvailable() (version.Update, bool) {
	var latest string
	switch {
	case e.Updates != nil:
		latest = e.Updates().LatestRelease
	case e.Index != nil:
		st, _ := published.ReadState(e.Index.Dir)
		latest = st.LatestRelease
	}
	return version.Available(latest, e.Info.Version)
}

// UpdateNote adds a note about a newer release to the first tool result of each
// session, for the assistant to pass on to the user.
func (e *Env) UpdateNote() mcp.Middleware { return e.updateNote(&sync.Map{}) }

// updateNote is UpdateNote, keeping in noted the sessions given the note, or found up
// to date.
func (e *Env) updateNote(noted *sync.Map) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			res, err := next(ctx, method, req)
			if err != nil || method != "tools/call" {
				return res, err
			}
			r, ok := res.(*mcp.CallToolResult)
			if !ok || r.IsError {
				return res, err
			}
			session := req.GetSession()
			if _, seen := noted.LoadOrStore(session, true); seen {
				return res, err
			}
			// Forget the session when it ends, so a long-running HTTP server doesn't
			// keep every session it has had.
			if ss, ok := session.(*mcp.ServerSession); ok {
				go func() {
					_ = ss.Wait()
					noted.Delete(ss)
				}()
			}
			if u, ok := e.UpdateAvailable(); ok {
				r.Content = append(r.Content, &mcp.TextContent{Text: "Note for the user (once): " + u.Message()})
			}
			return res, err
		}
	}
}
