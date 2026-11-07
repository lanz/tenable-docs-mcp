// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

//lint:file-ignore SA1019 MCP log messages are deprecated as of protocol version 2026-07-28 (SEP-2577) but still part of it, and clients on older versions use them.

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// connectFull connects a client to a server with completions, as server.New sets it up.
func connectFull(t *testing.T, env *Env, opts *mcp.ClientOptions, protocol string) *mcp.ClientSession {
	t.Helper()
	s := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "0"}, &mcp.ServerOptions{CompletionHandler: env.Complete})
	Register(s, env)
	st, ct := mcp.NewInMemoryTransports()
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, opts).Connect(ctx, ct, &mcp.ClientSessionOptions{ProtocolVersion: protocol})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func readResource(t *testing.T, cs *mcp.ClientSession, uri string) (string, error) {
	t.Helper()
	res, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
	if err != nil {
		return "", err
	}
	if len(res.Contents) != 1 || res.Contents[0].URI != uri {
		t.Fatalf("%s: contents = %+v", uri, res.Contents)
	}
	return res.Contents[0].Text, nil
}

func TestResources(t *testing.T) {
	cs := connectFull(t, NewEnv(Info{Name: "tenable-docs-mcp", Version: "test"}, buildIndex(t)), nil, "")
	list, err := cs.ListResources(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var uris []string
	for _, r := range list.Resources {
		uris = append(uris, r.URI)
	}
	if got := strings.Join(uris, " "); !strings.Contains(got, statusURI) || !strings.Contains(got, "tenable-docs://guide/nessus/toc") ||
		!strings.Contains(got, "tenable-docs://guide/security-center/toc") || strings.Contains(got, "10_5") {
		t.Errorf("resources = %s", got)
	}
	tmpls, err := cs.ListResourceTemplates(ctx, nil)
	if err != nil || len(tmpls.ResourceTemplates) != 2 {
		t.Fatalf("templates = %+v, %v", tmpls, err)
	}

	text, err := readResource(t, cs, statusURI)
	var st statusOut
	if err != nil || json.Unmarshal([]byte(text), &st) != nil || st.Index.State != "ready" || st.Version != "test" {
		t.Errorf("status = %s, %v", text, err)
	}
	// A page resource is the whole page, however long.
	text, err = readResource(t, cs, PageURI(en+"nessus/Content/Install.htm"))
	if err != nil || !strings.Contains(text, "Install Nessus on Linux") || !strings.Contains(text, "Run the installer package") {
		t.Errorf("page = %q, %v", text, err)
	}
	text, err = readResource(t, cs, PageURI(en+"nessus/Content/Long.htm"))
	if err != nil || strings.Count(text, "repeated to make it long") != 40 {
		t.Errorf("long page: %d repeats, %v", strings.Count(text, "repeated to make it long"), err)
	}
	if text, err = readResource(t, cs, "tenable-docs://page/jp.docs.tenable.com/nessus/Content/Install.htm"); err != nil || !strings.Contains(text, "インストーラー") {
		t.Errorf("jp page = %q, %v", text, err)
	}
	text, err = readResource(t, cs, TocURI("nessus", ""))
	if err != nil || !strings.Contains(text, "- [Welcome to Nessus]("+en+"nessus/Content/Welcome.htm)\n  - [Install Nessus on Linux]("+en+"nessus/Content/Install.htm)") {
		t.Errorf("toc = %q, %v", text, err)
	}
	// The template serves guides that aren't listed, such as other languages.
	if _, err := readResource(t, cs, TocURI("nessus", "jp")); err == nil {
		t.Error("the jp index has no nessus table of contents")
	}
	for _, uri := range []string{
		PageURI(en + "nessus/Content/Missing.htm"), "tenable-docs://page/example.com/x.htm",
		TocURI("missing", ""), "tenable-docs://nothing",
	} {
		if _, err := readResource(t, cs, uri); err == nil {
			t.Errorf("%s should fail", uri)
		}
	}
	if got := TocURI("/security-center/6_8/", "en"); got != "tenable-docs://guide/security-center/6_8/toc" {
		t.Errorf("TocURI = %s", got)
	}

	// Without an index, only the status is listed.
	cs = connectFull(t, NewEnv(Info{}, nil), nil, "")
	list, _ = cs.ListResources(ctx, nil)
	if len(list.Resources) != 1 {
		t.Errorf("without an index = %+v", list.Resources)
	}
	if text, err := readResource(t, cs, statusURI); err != nil || !strings.Contains(text, `"not built"`) {
		t.Errorf("status without an index = %s, %v", text, err)
	}
}

func getPrompt(t *testing.T, cs *mcp.ClientSession, name string, a map[string]string) (string, error) {
	t.Helper()
	res, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: name, Arguments: a})
	if err != nil {
		return "", err
	}
	if len(res.Messages) != 1 || res.Messages[0].Role != "user" {
		t.Fatalf("%s: messages = %+v", name, res.Messages)
	}
	return res.Messages[0].Content.(*mcp.TextContent).Text, nil
}

func TestPrompts(t *testing.T) {
	cs := connectFull(t, NewEnv(Info{}, nil), nil, "")
	list, err := cs.ListPrompts(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range list.Prompts {
		names = append(names, p.Name)
		if p.Title == "" || p.Description == "" || len(p.Arguments) == 0 || !p.Arguments[0].Required {
			t.Errorf("%s: %+v", p.Name, p)
		}
	}
	slices.Sort(names)
	if got := strings.Join(names, " "); got != "check-my-answer explain-setting how-to troubleshoot upgrade-check whats-new who-can" {
		t.Errorf("prompts = %s", got)
	}
	for _, tc := range []struct {
		name string
		args map[string]string
		want []string
	}{
		{"how-to", map[string]string{"task": " link an agent ", "product": "Nessus", "version": "10.8", "language": "fr", "guide_type": "User Guide"},
			[]string{"How do I link an agent?", "The product is Nessus.", "The version is 10.8.", "User Guide", "fr docs", "search_docs", "verify_citations", "report_gap"}},
		{"explain-setting", map[string]string{"setting": "Max simultaneous hosts per scan"}, []string{`"Max simultaneous hosts per scan"`, "find_setting"}},
		{"troubleshoot", map[string]string{"problem": "Plugins failed to compile"}, []string{"Plugins failed to compile", "release_notes"}},
		{"whats-new", map[string]string{"product": "Security Center"}, []string{"What's new in Security Center the latest release?", "release_notes"}},
		{"who-can", map[string]string{"action": "create a scan"}, []string{"need to create a scan?", "required_role"}},
		{"upgrade-check", map[string]string{"product": "Nessus", "from_version": "10.5"}, []string{"from 10.5 to the latest version", "requirements"}},
		{"check-my-answer", map[string]string{"answer": "Use port 8834.", "question": "Which port?"}, []string{"Question: Which port?", "Use port 8834.", "verify_citations"}},
	} {
		text, err := getPrompt(t, cs, tc.name, tc.args)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		for _, w := range tc.want {
			if !strings.Contains(text, w) {
				t.Errorf("%s lacks %q:\n%s", tc.name, w, text)
			}
		}
	}
	for _, tc := range []struct {
		name string
		args map[string]string
	}{{"how-to", nil}, {"how-to", map[string]string{"task": "  "}}, {"upgrade-check", map[string]string{"product": "Nessus"}}, {"nope", nil}} {
		if _, err := getPrompt(t, cs, tc.name, tc.args); err == nil {
			t.Errorf("%s %v should fail", tc.name, tc.args)
		}
	}
}

func TestCompletions(t *testing.T) {
	cs := connectFull(t, NewEnv(Info{}, buildIndex(t)), nil, "")
	complete := func(ref *mcp.CompleteReference, name, value string, given map[string]string) []string {
		t.Helper()
		res, err := cs.Complete(ctx, &mcp.CompleteParams{Ref: ref, Argument: mcp.CompleteParamsArgument{Name: name, Value: value}, Context: &mcp.CompleteContext{Arguments: given}})
		if err != nil {
			t.Fatalf("%s %q: %v", name, value, err)
		}
		if res.Completion.Values == nil || res.Completion.Total != len(res.Completion.Values) {
			t.Errorf("%s %q: %+v", name, value, res.Completion)
		}
		return res.Completion.Values
	}
	prompt := &mcp.CompleteReference{Type: "ref/prompt", Name: "how-to"}
	toc := &mcp.CompleteReference{Type: "ref/resource", URI: tocTmpl}
	for _, tc := range []struct {
		ref         *mcp.CompleteReference
		name, value string
		given       map[string]string
		want        string
	}{
		{prompt, "product", "nes", nil, "Nessus"},
		{prompt, "product", "SECURITY", nil, "Security Center"},
		{prompt, "language", "j", nil, "jp"},
		{prompt, "guide_type", "", nil, "User Guide"},
		{prompt, "version", "", map[string]string{"product": "Tenable.sc"}, ""},
		{prompt, "version", "", map[string]string{"product": "nessus"}, "10.5"},
		{prompt, "version", "1", nil, "10.5"},
		{&mcp.CompleteReference{Type: "ref/prompt", Name: "upgrade-check"}, "from_version", "", map[string]string{"product": "Nessus"}, "10.5"},
		{toc, "guide", "", nil, "nessus security-center nessus/10_5"},
		{toc, "guide", "sec", nil, "security-center"},
		{toc, "guide", "", map[string]string{"language": "jp"}, "nessus"},
		{toc, "guide", "", map[string]string{"guide": "nessus"}, "nessus security-center nessus/10_5"},
		{prompt, "task", "inst", nil, ""},
	} {
		got := complete(tc.ref, tc.name, tc.value, tc.given)
		if strings.Join(got, " ") != tc.want && !(tc.name == "product" && len(got) > 0 && got[0] == tc.want) {
			t.Errorf("%s %q %v = %v, want %s", tc.name, tc.value, tc.given, got, tc.want)
		}
	}
	// No index: products and languages still complete.
	cs = connectFull(t, NewEnv(Info{}, nil), nil, "")
	if got := complete(prompt, "language", "", nil); len(got) != 8 {
		t.Errorf("languages = %v", got)
	}
	if got := complete(prompt, "guide_type", "", nil); len(got) != 0 {
		t.Errorf("guide types without an index = %v", got)
	}
}

func TestMatchingCap(t *testing.T) {
	var many []string
	for i := range 150 {
		many = append(many, fmt.Sprintf("guide-%03d", i))
	}
	if got := matching(many, "GUIDE-1"); len(got) != 50 || got[0] != "guide-100" {
		t.Errorf("matching = %v", got)
	}
	if got := matching([]string{"a-nessus", "nessus"}, "nes"); strings.Join(got, " ") != "nessus a-nessus" {
		t.Errorf("prefix first = %v", got)
	}
}

func TestRefreshProgressAndLogs(t *testing.T) {
	env := NewEnv(Info{Name: "tenable-docs-mcp"}, buildIndex(t))
	env.AllowRefresh = true
	env.Live = liveSite{
		en + "robots.txt":                 "User-agent: *\nDisallow:\n",
		en + "nessus/Content/Install.htm": `<div id="mc-main-content"><h1>Install Nessus on Linux</h1><p>New.</p></div>`,
	}
	var mu sync.Mutex
	var progress, logs []string
	opts := &mcp.ClientOptions{
		ProgressNotificationHandler: func(_ context.Context, r *mcp.ProgressNotificationClientRequest) {
			mu.Lock()
			defer mu.Unlock()
			progress = append(progress, fmt.Sprintf("%v/%v %s", r.Params.Progress, r.Params.Total, r.Params.Message))
		},
		LoggingMessageHandler: func(_ context.Context, r *mcp.LoggingMessageRequest) {
			mu.Lock()
			defer mu.Unlock()
			logs = append(logs, fmt.Sprintf("%s %s: %v", r.Params.Level, r.Params.Logger, r.Params.Data))
		},
	}
	// Log levels are set per session on protocol versions before 2026-07-28.
	cs := connectFull(t, env, opts, "2025-11-25")
	if err := cs.SetLoggingLevel(ctx, &mcp.SetLoggingLevelParams{Level: "info"}); err != nil {
		t.Fatal(err)
	}
	params := &mcp.CallToolParams{Name: "refresh_index", Arguments: map[string]any{"urls": []string{en + "nessus/Content/Install.htm", en + "nessus/Content/Ports.htm"}}}
	params.SetProgressToken("refresh-1")
	if res, err := cs.CallTool(ctx, params); err != nil || res.IsError {
		t.Fatalf("refresh = %+v, %v", res, err)
	}
	// Notifications can arrive just after the result.
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n, m := len(progress), len(logs)
		mu.Unlock()
		if n >= 2 && m >= 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(progress, "; ") != "1/2 Checking pages: 1 of 2; 2/2 Checking pages: 2 of 2" {
		t.Errorf("progress = %v", progress)
	}
	if len(logs) != 1 || !strings.HasPrefix(logs[0], "info tenable-docs-mcp: Checked 2 pages: 1 updated") {
		t.Errorf("logs = %v", logs)
	}
	// Without a progress token, there's no progress.
	progress = nil
	mu.Unlock()
	call(t, cs, "refresh_index", map[string]any{"urls": []string{en + "nessus/Content/Install.htm"}}, nil)
	mu.Lock()
	if len(progress) != 0 {
		t.Errorf("progress without a token = %v", progress)
	}
}
