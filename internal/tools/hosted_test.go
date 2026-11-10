// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lanz/tenable-docs-mcp/internal/published"
)

// connectHosted connects a client to a hosted server's tools, with env's index.
func connectHosted(t *testing.T, env *Env) *mcp.ClientSession {
	t.Helper()
	env.Hosted = true
	s := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "0"}, nil)
	Register(s, env)
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

// A hosted server's index_status reports what the index holds, and none of its own
// details: no folder, download address, error, local advice, or update note.
func TestHostedStatus(t *testing.T) {
	ix := buildIndex(t)
	env := NewEnv(Info{Name: "tenable-docs-mcp", Version: "v1.0.0"}, ix)
	env.Updates = func() published.Status {
		return published.Status{State: published.State{URL: "https://mirror.internal.example/index/v1/",
			CheckedAt: "2026-11-09T12:00:00Z", BuiltAt: "2026-11-09T11:00:00Z",
			LastError: "dial tcp 10.1.2.3:443: refused", LatestRelease: "v9.9.9"}}
	}
	cs := connectHosted(t, env)
	var out statusOut
	text := call(t, cs, "index_status", nil, &out)
	raw, _ := json.Marshal(out)
	all := text + string(raw)
	for _, leak := range []string{ix.Dir, "mirror.internal.example", "10.1.2.3", "v9.9.9", "index pull", "live sites"} {
		if strings.Contains(all, leak) {
			t.Errorf("hosted status shows %q:\n%s\n%s", leak, text, raw)
		}
	}
	if out.Index.State != "ready" || len(out.Index.Shards) == 0 || !strings.Contains(text, "(hosted)") ||
		!strings.Contains(text, "last checked 2026-11-09T12:00:00Z") {
		t.Errorf("hosted status:\n%s", text)
	}
}

// A hosted server's verify_citations doesn't offer check_live.
func TestHostedVerifyHasNoCheckLive(t *testing.T) {
	cs := connectHosted(t, NewEnv(Info{Name: "tenable-docs-mcp", Version: "v1.0.0"}, buildIndex(t)))
	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range res.Tools {
		if tool.Name != "verify_citations" {
			continue
		}
		schema, _ := json.Marshal(tool.InputSchema)
		if strings.Contains(string(schema), "check_live") || !strings.Contains(string(schema), `"text"`) {
			t.Errorf("schema = %s", schema)
		}
		if tool.Annotations.OpenWorldHint == nil || *tool.Annotations.OpenWorldHint {
			t.Error("a hosted verify_citations shouldn't be marked as reaching outside")
		}
		return
	}
	t.Fatal("no verify_citations")
}
