// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lanz/tenable-docs-mcp/internal/feedback"
	"github.com/lanz/tenable-docs-mcp/internal/gaps"
	"github.com/lanz/tenable-docs-mcp/internal/index"
)

const en = "https://docs.tenable.com/"

// widgetIndex builds the made-up sites into an index.
func widgetIndex(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "index")
	if code, _, errOut := runCmd("index", "build", "-source", testSources(t), "-index", dir); code != 0 {
		t.Fatalf("index build: %d %s", code, errOut)
	}
	return dir
}

// callDirect calls a tool through an MCP session, as an assistant would, for comparing
// with the command's output.
func callDirect(t *testing.T, dir, tool string, args map[string]any) (string, any) {
	t.Helper()
	ix, err := index.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	cs, closeSession, err := toolSession(context.Background(), ix)
	if err != nil {
		t.Fatal(err)
	}
	defer closeSession()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	return resultText(res), res.StructuredContent
}

func TestToolCommandsMatchTheTools(t *testing.T) {
	dir := widgetIndex(t)
	for _, c := range []struct {
		args []string
		tool string
		in   map[string]any
		code int
	}{
		{[]string{"search", "calibration", "dial"}, "search_docs", map[string]any{"query": "calibration dial"}, 0},
		{[]string{"search", "-limit", "1", "sprockets"}, "search_docs", map[string]any{"query": "sprockets", "limit": 1}, 0},
		{[]string{"search", "zzqqxx"}, "search_docs", map[string]any{"query": "zzqqxx"}, 1},
		{[]string{"read", en + "widget/Content/Setup.htm#Ports"}, "read_doc", map[string]any{"url": en + "widget/Content/Setup.htm#Ports"}, 0},
		{[]string{"outline", en + "widget/Content/Setup.htm"}, "outline", map[string]any{"url": en + "widget/Content/Setup.htm"}, 0},
		{[]string{"browse", "widget"}, "browse_guide", map[string]any{"guide": "widget"}, 0},
		{[]string{"related", en + "widget/Content/Start.htm"}, "related_pages", map[string]any{"url": en + "widget/Content/Start.htm"}, 0},
		{[]string{"products"}, "list_products", map[string]any{}, 0},
		{[]string{"setting", "calibration", "dial"}, "find_setting", map[string]any{"query": "calibration dial"}, 1},
		{[]string{"role", "set", "up", "widgets"}, "required_role", map[string]any{"task": "set up widgets"}, 0},
		{[]string{"requirements", "-kind", "browsers"}, "requirements", map[string]any{"kind": "browsers"}, 1},
		{[]string{"release-notes"}, "release_notes", map[string]any{}, 1},
		{[]string{"term", "sprocket"}, "define_term", map[string]any{"term": "sprocket"}, 1},
		{[]string{"status"}, "index_status", map[string]any{}, 0},
	} {
		t.Run(strings.Join(c.args, " "), func(t *testing.T) {
			code, out, errOut := runCmd(append(c.args, "-index", dir)...)
			if code != c.code {
				t.Fatalf("exit %d, want %d; stderr %q", code, c.code, errOut)
			}
			want, _ := callDirect(t, dir, c.tool, c.in)
			if strings.TrimRight(out, "\n") != strings.TrimRight(want, "\n") {
				t.Errorf("output differs from %s:\n got %q\nwant %q", c.tool, out, want)
			}
		})
	}
}

func TestToolCommandJSON(t *testing.T) {
	dir := widgetIndex(t)
	code, out, _ := runCmd("search", "-json", "calibration", "-index", dir)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	_, want := callDirect(t, dir, "search_docs", map[string]any{"query": "calibration"})
	a, _ := json.Marshal(got)
	b, _ := json.Marshal(want)
	if string(a) != string(b) {
		t.Errorf("JSON differs from search_docs' structured output:\n%s\n%s", a, b)
	}
}

func TestToolCommandsWithoutAnIndex(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "empty")
	code, _, errOut := runCmd("search", "nessus", "-index", dir)
	if code != 1 || !strings.Contains(errOut, "isn't built yet") || !strings.Contains(errOut, "index pull") {
		t.Errorf("search: %d %q", code, errOut)
	}
	// status still runs, and says so.
	code, out, _ := runCmd("status", "-index", dir)
	if code != 1 || !strings.Contains(out, "not built") {
		t.Errorf("status: %d %q", code, out)
	}
	// TDM_INDEX_DIR works as -index does.
	t.Setenv("TDM_INDEX_DIR", widgetIndex(t))
	if code, out, errOut := runCmd("search", "sprockets"); code != 0 || !strings.Contains(out, "Widget Manager") {
		t.Errorf("with TDM_INDEX_DIR: %d %q %q", code, out, errOut)
	}
}

func TestToolCommandUsage(t *testing.T) {
	dir := widgetIndex(t)
	for _, args := range [][]string{
		{"search"},                         // no question
		{"search", "-nope", "x"},           // no such flag
		{"products", "extra", "words"},     // no words
		{"read"},                           // no URL
		{"setting", "-limit", "x", "dial"}, // not a number
	} {
		if code, _, _ := runCmd(append(args, "-index", dir)...); code != 2 {
			t.Errorf("%q: exit %d, want 2", args, code)
		}
	}
	code, _, errOut := runCmd("search", "-h")
	if code != 0 || !strings.Contains(errOut, "-product string") || !strings.Contains(errOut, "-release-notes") {
		t.Errorf("search -h: %d %q", code, errOut)
	}
	// help lists every command.
	_, help, _ := runCmd("help")
	for _, c := range toolCommands {
		if !strings.Contains(help, "tenable-docs-mcp "+c.Name+" ") {
			t.Errorf("help doesn't list %s", c.Name)
		}
	}
	for _, c := range []string{"doctor", "config", "gaps submit"} {
		if !strings.Contains(help, "tenable-docs-mcp "+c) {
			t.Errorf("help doesn't list %s", c)
		}
	}
}

func TestParseInterspersed(t *testing.T) {
	for _, c := range []struct {
		args  []string
		words string
		limit int
	}{
		{[]string{"install", "nessus", "-limit", "3"}, "install nessus", 3},
		{[]string{"-limit", "3", "install", "nessus"}, "install nessus", 3},
		{[]string{"install", "-limit=3", "nessus"}, "install nessus", 3},
		{[]string{"install", "--", "-limit", "3"}, "install -limit 3", 0},
	} {
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		limit := fs.Int("limit", 0, "")
		words, err := parseInterspersed(fs, c.args)
		if err != nil || strings.Join(words, " ") != c.words || *limit != c.limit {
			t.Errorf("%q: %q, limit %d, %v", c.args, words, *limit, err)
		}
	}
}

// configBin is a path with a space in it, as this platform writes paths, and as a
// shell command needs it.
func configBin() (bin, slashed string) {
	if runtime.GOOS == "windows" {
		return `C:\Program Files\Tenable Docs\tenable-docs-mcp.exe`, "C:/Program Files/Tenable Docs/tenable-docs-mcp.exe"
	}
	return "/opt/Tenable Docs/tenable-docs-mcp", "/opt/Tenable Docs/tenable-docs-mcp"
}

func TestConfig(t *testing.T) {
	bin, slashed := configBin()
	executable = func() (string, error) { return bin, nil }
	t.Cleanup(func() { executable = func() (string, error) { return os.Executable() } })
	code, out, errOut := runCmd("config", "-index", filepath.Join(t.TempDir(), "my index"))
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	// Claude Code: a shell command, with forward slashes and the path quoted. The name
	// comes before -e, which would otherwise take it as another value.
	if !strings.Contains(out, `claude mcp add --scope user tenable-docs -e "TDM_INDEX_DIR=`) ||
		!strings.Contains(out, `my index" -- "`+slashed+`"`) {
		t.Errorf("claude-code:\n%s", out)
	}
	// The JSON clients: each block parses, with the path as given.
	blocks := strings.Split(out, "\n# ")
	jsons := 0
	for _, b := range blocks {
		start := strings.Index(b, "{")
		if start < 0 {
			continue
		}
		var cfg map[string]map[string]map[string]any
		if err := json.Unmarshal([]byte(b[start:]), &cfg); err != nil {
			t.Errorf("not JSON: %v\n%s", err, b)
			continue
		}
		for _, servers := range cfg {
			entry := servers["tenable-docs"]
			if entry["command"] != bin {
				t.Errorf("command = %v", entry["command"])
			}
			if env, _ := entry["env"].(map[string]any); !strings.HasSuffix(env["TDM_INDEX_DIR"].(string), "my index") {
				t.Errorf("env = %v", entry["env"])
			}
		}
		jsons++
	}
	// On Windows, JSON needs the backslashes doubled.
	if jsons != 3 || !strings.Contains(out, `"type": "stdio"`) || runtime.GOOS == "windows" && !strings.Contains(out, `C:\\Program Files`) {
		t.Errorf("%d JSON blocks:\n%s", jsons, out)
	}
	// One client, and a bad one.
	if code, out, _ := runCmd("config", "-client", "cursor"); code != 0 || strings.Contains(out, "claude mcp add") || !strings.Contains(out, "~/.cursor/mcp.json") {
		t.Errorf("-client cursor: %d %q", code, out)
	}
	if code, _, _ := runCmd("config", "-client", "emacs"); code != 2 {
		t.Errorf("-client emacs: %d", code)
	}
}

func TestShellQuote(t *testing.T) {
	for in, want := range map[string]string{
		"/usr/local/bin/tenable-docs-mcp": "/usr/local/bin/tenable-docs-mcp",
		"C:/Program Files/x.exe":          `"C:/Program Files/x.exe"`,
		`it's "odd"`:                      `'it'\''s "odd"'`,
	} {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDoctor(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/gone" {
			http.NotFound(w, r)
			return
		}
		io.WriteString(w, "ok")
	}))
	defer srv.Close()
	oldURLs, oldClient := doctorURLs, doctorClient
	t.Cleanup(func() { doctorURLs, doctorClient = oldURLs, oldClient })
	doctorClient = srv.Client()
	doctorURLs = []string{srv.URL + "/robots.txt"}

	dir := widgetIndex(t)
	code, out, _ := runCmd("doctor", "-index", dir)
	if code != 0 || !strings.Contains(out, "✓ the index opens: 3 shards") {
		t.Errorf("doctor: %d\n%s", code, out)
	}
	// httptest's certificate isn't from a public CA, as a TLS-inspecting proxy's isn't.
	if !strings.Contains(out, "through a TLS-inspecting proxy") {
		t.Errorf("no proxy note:\n%s", out)
	}

	// An index that isn't built, and a site that fails.
	doctorURLs = []string{srv.URL + "/gone"}
	code, out, _ = runCmd("doctor", "-index", filepath.Join(t.TempDir(), "none"))
	if code != 1 || !strings.Contains(out, "✗ the index folder") || !strings.Contains(out, "HTTP 404") {
		t.Errorf("failing doctor: %d\n%s", code, out)
	}

	// A certificate this computer doesn't trust.
	doctorClient = &http.Client{Timeout: 5 * time.Second}
	doctorURLs = []string{srv.URL + "/robots.txt"}
	code, out, _ = runCmd("doctor", "-index", dir)
	if code != 1 || !strings.Contains(out, "doesn't trust") {
		t.Errorf("untrusted certificate: %d\n%s", code, out)
	}
}

// fakeFeedback is a feedback endpoint that records what it receives.
type fakeFeedback struct {
	*httptest.Server
	mu     sync.Mutex
	bodies []map[string]any
}

func newFakeFeedback(t *testing.T) *fakeFeedback {
	f := &fakeFeedback{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.bodies = append(f.bodies, body)
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok": true}`)
	}))
	t.Cleanup(f.Close)
	return f
}

func TestGapsSubmit(t *testing.T) {
	f := newFakeFeedback(t)
	logFile := filepath.Join(t.TempDir(), "feedback.jsonl")
	old := gapsSender
	// Two minutes pass between sends, so the one-a-minute limit doesn't apply.
	clock := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	gapsSender = func() (*feedback.Sender, error) {
		return &feedback.Sender{Endpoint: f.URL, UserAgent: "test", LogFile: logFile,
			Now: func() time.Time { clock = clock.Add(2 * time.Minute); return clock }}, nil
	}
	t.Cleanup(func() { gapsSender = old })

	file := filepath.Join(t.TempDir(), "gaps.jsonl")
	day := func(d int) time.Time { return time.Date(2026, 11, d, 12, 0, 0, 0, time.UTC) }
	for _, r := range []gaps.Record{
		{Time: day(1), Question: "How do I recalibrate a sprocket?", Product: "Widget Manager", URL: en + "widget/Content/Setup.htm"},
		{Time: day(2), Question: "Can widgets run offline?", Product: "Widget Manager"},
		{Time: day(3), Question: "Old question already sent", SentAt: day(4)},
		{Time: day(5), Question: "Does Widget Manager support SAML?"},
	} {
		if _, err := gaps.Add(file, r); err != nil {
			t.Fatal(err)
		}
	}
	dir := widgetIndex(t)

	// Send the first as it is, edit the second, and skip the third.
	stdin = strings.NewReader("y\nI was setting up a new site\n\ny\n" +
		"e\nCan widgets run without a network?\n\nme@example.com\ny\n" +
		"s\n")
	t.Cleanup(func() { stdin = os.Stdin })
	code, out, errOut := runCmd("gaps", "submit", "-file", file, "-index", dir)
	if code != 0 || !strings.Contains(out, "3 recorded questions haven't been sent") || !strings.Contains(out, "Sent 2; skipped 1.") {
		t.Fatalf("submit: %d\n%s\n%s", code, out, errOut)
	}
	if strings.Contains(out, "Old question already sent") {
		t.Error("offered a question that was already sent")
	}
	if !strings.Contains(out, "This is what would be sent:") || !strings.Contains(out, "Page: "+en+"widget/Content/Setup.htm") {
		t.Errorf("no preview or page:\n%s", out)
	}
	if len(f.bodies) != 2 {
		t.Fatalf("the endpoint got %d requests", len(f.bodies))
	}
	second, _ := json.Marshal(f.bodies[1])
	if !strings.Contains(string(second), "Can widgets run without a network?") || !strings.Contains(string(second), "me@example.com") {
		t.Errorf("the edited question wasn't sent: %s", second)
	}

	// The sent ones are marked; the skipped one is offered again, and -since filters.
	recs, _ := gaps.Read(file)
	sent := 0
	for _, r := range recs {
		if !r.SentAt.IsZero() {
			sent++
		}
	}
	if sent != 3 {
		t.Errorf("%d marked sent, want 3", sent)
	}
	stdin = strings.NewReader("q\n")
	code, out, _ = runCmd("gaps", "submit", "-file", file, "-since", "2026-11-05")
	if code != 0 || !strings.Contains(out, "1 recorded questions") || !strings.Contains(out, "SAML") || !strings.Contains(out, "Sent 0") {
		t.Errorf("second run: %d\n%s", code, out)
	}
	// Nothing left before that date.
	stdin = strings.NewReader("")
	if code, out, _ := runCmd("gaps", "submit", "-file", file, "-since", "2026-12-01"); code != 0 || !strings.Contains(out, "No recorded questions") {
		t.Errorf("none: %d %q", code, out)
	}
}

func TestGapsSubmitStopsAtTheLimit(t *testing.T) {
	f := newFakeFeedback(t)
	logFile := filepath.Join(t.TempDir(), "feedback.jsonl")
	old := gapsSender
	gapsSender = func() (*feedback.Sender, error) {
		return &feedback.Sender{Endpoint: f.URL, UserAgent: "test", LogFile: logFile}, nil
	}
	t.Cleanup(func() { gapsSender = old })
	file := filepath.Join(t.TempDir(), "gaps.jsonl")
	for _, q := range []string{"First question", "Second question"} {
		if _, err := gaps.Add(file, gaps.Record{Question: q}); err != nil {
			t.Fatal(err)
		}
	}
	// One a minute: the second is refused, and stays unsent.
	stdin = strings.NewReader("y\n\n\ny\ny\n\n\ny\n")
	t.Cleanup(func() { stdin = os.Stdin })
	code, out, _ := runCmd("gaps", "submit", "-file", file, "-index", filepath.Join(t.TempDir(), "none"))
	if code != 1 || !strings.Contains(out, "Not sent:") || len(f.bodies) != 1 {
		t.Errorf("limit: %d, %d sent\n%s", code, len(f.bodies), out)
	}
	recs, _ := gaps.Read(file)
	if recs[1].SentAt.IsZero() == false {
		t.Error("the refused question was marked sent")
	}
}
