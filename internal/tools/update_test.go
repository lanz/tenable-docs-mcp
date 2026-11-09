// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lanz/tenable-docs-mcp/internal/index"
	"github.com/lanz/tenable-docs-mcp/internal/published"
)

// connectAs connects a client to a server of the given version, with the update note.
func connectAs(t *testing.T, ix *index.Index, v string) *mcp.ClientSession {
	t.Helper()
	env := NewEnv(Info{Name: "tenable-docs-mcp", Version: v}, ix)
	s := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: v}, nil)
	Register(s, env)
	s.AddReceivingMiddleware(env.UpdateNote())
	st, ct := mcp.NewInMemoryTransports()
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func text(r *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range r.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text + "\n")
		}
	}
	return b.String()
}

func TestUpdateNotice(t *testing.T) {
	ix := buildIndex(t)
	if err := published.WriteState(ix.Dir, published.State{LatestRelease: "v1.1.0"}); err != nil {
		t.Fatal(err)
	}
	search := func(cs *mcp.ClientSession) string {
		t.Helper()
		r, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "search_docs", Arguments: map[string]any{"query": "install nessus"}})
		if err != nil || r.IsError {
			t.Fatalf("search = %v, %v", r, err)
		}
		return text(r)
	}

	// An older release hears about it in its first result, and only its first.
	cs := connectAs(t, ix, "v1.0.0")
	if first := search(cs); !strings.Contains(first, "Note for the user (once): Tenable Docs MCP v1.1.0 is available; this is v1.0.0") {
		t.Errorf("first result lacks the note:\n%s", first)
	}
	if second := search(cs); strings.Contains(second, "is available") {
		t.Errorf("second result repeats the note:\n%s", second)
	}
	var st statusOut
	if txt := call(t, cs, "index_status", nil, &st); st.Update == nil || st.Update.Latest != "v1.1.0" || st.Update.Current != "v1.0.0" ||
		!strings.Contains(st.Update.URL, "/releases/tag/v1.1.0") || !strings.Contains(txt, "v1.1.0 is available") {
		t.Errorf("status update = %+v\n%s", st.Update, txt)
	}
	// A new session hears about it again.
	if first := search(connectAs(t, ix, "v1.0.0")); !strings.Contains(first, "is available") {
		t.Errorf("a new session's first result lacks the note:\n%s", first)
	}

	// The current release, and a development build, don't.
	for _, v := range []string{"v1.1.0", "test"} {
		cs := connectAs(t, ix, v)
		if first := search(cs); strings.Contains(first, "is available") {
			t.Errorf("%s: %s", v, first)
		}
		var st statusOut
		if call(t, cs, "index_status", nil, &st); st.Update != nil {
			t.Errorf("%s: status update = %+v", v, st.Update)
		}
	}
}
