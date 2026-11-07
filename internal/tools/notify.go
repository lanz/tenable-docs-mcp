// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

//lint:file-ignore SA1019 MCP log messages are deprecated as of protocol version 2026-07-28 (SEP-2577) but still part of it, and clients on older versions use them.

package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// progressFor returns a function that reports a tool call's progress to the client, or
// nil when the client didn't ask for progress.
func progressFor(ctx context.Context, req *mcp.CallToolRequest, what string) func(done, total int) {
	if req == nil || req.Session == nil || req.Params == nil {
		return nil
	}
	token := req.Params.GetProgressToken()
	if token == nil {
		return nil
	}
	return func(done, total int) {
		_ = req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{
			ProgressToken: token, Progress: float64(done), Total: float64(total),
			Message: fmt.Sprintf("%s: %d of %d", what, done, total),
		})
	}
}

// logf sends a log message to the client, which receives it only at a level it asked
// for.
func (e *Env) logf(ctx context.Context, ss *mcp.ServerSession, level mcp.LoggingLevel, format string, args ...any) {
	if ss == nil {
		return
	}
	_ = ss.Log(ctx, &mcp.LoggingMessageParams{Level: level, Logger: e.Info.Name, Data: fmt.Sprintf(format, args...)})
}
