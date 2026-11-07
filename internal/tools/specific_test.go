// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lanz/tenable-docs-mcp/internal/index"
)

var (
	enCur = index.Key{Site: "en"}
	enOld = index.Key{Site: "en", Older: true}
)

// Made-up pages with the tables and structure the specialized tools read.
var specificFixture = []fixturePage{
	{enCur, "nessus/Content/ScanSettings.htm", []string{"Nessus"}, `<h1>Advanced Scan Settings</h1><h2 id="Perf">Performance</h2>
		<table><thead><tr><th>Setting</th><th>Default</th><th>Description</th></tr></thead><tbody>
		<tr><td>Max simultaneous hosts per scan</td><td>30</td><td>The most hosts a scanner scans at once.</td></tr>
		<tr><td>Max simultaneous checks per host</td><td>5</td><td>The most checks against one host at once.</td></tr>
		</tbody></table>`},
	{enCur, "quick-reference/tuning/Content/Settings.htm", []string{"Nessus"}, `<h1>Tuning Settings</h1>
		<table><thead><tr><th>Setting</th><th>Description</th></tr></thead><tbody>
		<tr><td>Max simultaneous hosts per scan</td><td>Hosts a scanner scans per chunk.</td></tr></tbody></table>`},
	{enCur, "security-center/Content/Config.htm", []string{"Security Center"}, `<h1>Configuration Settings</h1><h2 id="Security">Security</h2>
		<table><thead><tr><th>Option</th><th>Description</th></tr></thead><tbody>
		<tr><td>Session Timeout</td><td>The web session timeout in minutes.</td></tr></tbody></table>`},
	{enOld, "security-center/6_5/Content/Config.htm", []string{"Security Center"}, `<h1>Configuration Settings</h1>
		<table><thead><tr><th>Option</th><th>Description</th></tr></thead><tbody>
		<tr><td>Session Timeout</td><td>The older web session timeout.</td></tr></tbody></table>`},
	{enCur, "security-center/Content/CreateScan.htm", []string{"Security Center"}, `<h1>Add an Active Scan</h1>
		<p class="Permission"><b>Required Tenable Security Center User Role:</b> Administrator or Scan Manager</p><p>Create a scan of your hosts.</p>`},
	{enCur, "nessus/Content/CreateScan.htm", []string{"Nessus"}, `<h1>Create a Scan</h1>
		<p class="Permission"><b>Required User Role:</b> Standard or Administrator</p><p>Create a scan in Nessus.</p>`},
	{enCur, "nessus/Content/Overview.htm", []string{"Nessus"}, `<h1>Plugin Overview</h1><p>Plugins check for vulnerabilities.</p>`},
	{enCur, "nessus/Content/PortRequirements.htm", []string{"Nessus"}, `<h1>Port Requirements</h1><h2 id="Ports">Nessus</h2>
		<table><thead><tr><th>Port</th><th>Traffic</th></tr></thead><tbody>
		<tr><td>TCP 8834</td><td>The web interface.</td></tr><tr><td>TCP 443</td><td>Cloud updates.</td></tr></tbody></table>`},
	{enCur, "nessus/Content/HardwareRequirements.htm", []string{"Nessus"}, `<h1>Hardware Requirements</h1>
		<table><thead><tr><th>Scenario</th><th>CPU Cores</th><th>Memory</th></tr></thead><tbody>
		<tr><td>Small</td><td>4</td><td>8 GB</td></tr></tbody></table>`},
	{enCur, "nessus/Content/CloudRequirements.htm", []string{"Nessus"}, `<h1>Cloud Requirements</h1>
		<table><thead><tr><th>Hosts</th><th>Instance Type</th></tr></thead><tbody><tr><td>500</td><td>m5.large</td></tr></tbody></table>`},
	{enOld, "nessus/10_5/Content/PortRequirements.htm", []string{"Nessus"}, `<h1>Port Requirements</h1>
		<table><thead><tr><th>Port</th><th>Traffic</th></tr></thead><tbody><tr><td>TCP 8834</td><td>The older web interface.</td></tr></tbody></table>`},
	{enCur, "release-notes/Content/nessus/2026.htm", []string{"Nessus"}, `<h1>Nessus 2026 Release Notes</h1>
		<h2 id="r2">Tenable Nessus 10.12.6 (2026-10-22)</h2><p>Fixed a scheduling bug.</p>
		<h2 id="r1">Tenable Nessus 10.12.5 (2026-10-01)</h2><p>Added a new dashboard.</p>
		<h2 id="r0">Tenable Nessus 10.11.2 (2026-01-15)</h2><p>Faster plugin updates.</p>`},
	{enCur, "glossary/Content/glossary.htm", []string{"A", "B", "C", "D"}, `<h1>Glossary</h1><ul class="MCGlossaryProxy_0">
		<li class="GlossaryPageEntry"><div class="GlossaryPageTerm"><a class="GlossaryPageLink" name="g1">Vulnerability Priority Rating (VPR)</a></div><div class="GlossaryPageDefinition">A risk score.</div></li>
		<li class="GlossaryPageEntry"><div class="GlossaryPageTerm"><a class="GlossaryPageLink" name="g2">National Vulnerability Database</a></div><div class="GlossaryPageDefinition">A public list of vulnerabilities.</div></li>
		<li class="GlossaryPageEntry"><div class="GlossaryPageTerm"><a class="GlossaryPageLink" name="g3">agent</a></div><div class="GlossaryPageDefinition">Software that scans its host.</div></li></ul>`},
	{enCur, "ai-exposure/Content/terms.htm", []string{"AI Exposure"}, `<h1>Key Terms</h1>
		<table><thead><tr><th>Term</th><th>Definition</th></tr></thead><tbody><tr><td>Agent</td><td>An AI system.</td></tr></tbody></table>`},
}

func TestFindSetting(t *testing.T) {
	cs := connect(t, buildIndexFrom(t, specificFixture))
	var out settingsOut
	text := call(t, cs, "find_setting", map[string]any{"query": "max simultaneous hosts in Nessus"}, &out)
	if len(out.Settings) < 2 || out.Product != "Nessus" {
		t.Fatalf("settings = %+v", out)
	}
	// The row with a default comes before the same setting without one.
	if last := out.Settings[len(out.Settings)-1]; last.Page != "Tuning Settings" || last.Default != "" {
		t.Errorf("last = %+v", last)
	}
	s := out.Settings[0]
	if s.Name != "Max simultaneous hosts per scan" || s.Default != "30" || !strings.HasSuffix(s.URL, "ScanSettings.htm#Perf") || s.Page != "Advanced Scan Settings" {
		t.Errorf("first = %+v", s)
	}
	if !strings.Contains(text, "default 30") {
		t.Errorf("summary = %q", text)
	}
	// A retired product name in the query, and a version.
	call(t, cs, "find_setting", map[string]any{"query": "Tenable.sc session timeout"}, &out)
	if len(out.Settings) != 1 || out.Settings[0].Product != "Security Center" || out.Settings[0].Version != "" {
		t.Errorf("sc = %+v", out)
	}
	call(t, cs, "find_setting", map[string]any{"query": "session timeout", "product": "Security Center", "version": "6.5"}, &out)
	if len(out.Settings) != 1 || out.Settings[0].Version != "6.5" {
		t.Errorf("6.5 = %+v", out)
	}
	call(t, cs, "find_setting", map[string]any{"query": "session timeout", "product": "Security Center", "version": "9.9"}, &out)
	if len(out.Settings) != 1 || out.Settings[0].Version != "" || len(out.Notes) != 1 {
		t.Errorf("9.9 = %+v", out)
	}
	// Not every word matches: the closest settings.
	call(t, cs, "find_setting", map[string]any{"query": "session timeout zebra", "product": "Security Center"}, &out)
	if len(out.Settings) == 0 {
		t.Errorf("relaxed = %+v", out)
	}
	// A product named in the question that has no match falls back to other products.
	call(t, cs, "find_setting", map[string]any{"query": "Nessus session timeout"}, &out)
	if len(out.Settings) == 0 || out.Settings[0].Product != "Security Center" || len(out.Notes) != 1 {
		t.Errorf("fallback = %+v", out)
	}
	call(t, cs, "find_setting", map[string]any{"query": "nothing like this", "product": "Nessus"}, &out)
	if len(out.Settings) != 0 || out.Message == "" {
		t.Errorf("none = %+v", out)
	}
	for _, args := range []map[string]any{{"query": "how do I"}, {"query": "x", "product": "Nope"}} {
		if text, isErr := callRaw(t, cs, "find_setting", args, nil); !isErr {
			t.Errorf("%v should fail: %s", args, text)
		}
	}
}

func TestRequiredRole(t *testing.T) {
	cs := connect(t, buildIndexFrom(t, specificFixture))
	var out rolesOut
	text := call(t, cs, "required_role", map[string]any{"task": "create a scan in Tenable.sc"}, &out)
	if len(out.Answers) != 1 || out.Answers[0].Product != "Security Center" || len(out.Answers[0].Required) != 1 {
		t.Fatalf("answers = %+v", out)
	}
	r := out.Answers[0].Required[0]
	if r.Label != "Required Tenable Security Center User Role" || !strings.Contains(r.Text, "Scan Manager") ||
		strings.Join(r.Roles, ",") != "Administrator,Scan Manager" || !strings.Contains(text, "Scan Manager") {
		t.Errorf("statement = %+v; %q", r, text)
	}
	call(t, cs, "required_role", map[string]any{"task": "create a scan"}, &out)
	if len(out.Answers) != 2 {
		t.Errorf("both products = %+v", out.Answers)
	}
	call(t, cs, "required_role", map[string]any{"task": "plugin overview"}, &out)
	if len(out.Answers) != 0 || out.Message == "" || len(out.Without) == 0 || out.Without[0].Title != "Plugin Overview" {
		t.Errorf("no roles = %+v", out)
	}
	if text, isErr := callRaw(t, cs, "required_role", map[string]any{"task": " "}, nil); !isErr {
		t.Errorf("empty task should fail: %s", text)
	}
}

func TestRequirementsTool(t *testing.T) {
	cs := connect(t, buildIndexFrom(t, specificFixture))
	var out requirementsResultOut
	text := call(t, cs, "requirements", map[string]any{"query": "Nessus ports"}, &out)
	if out.Product != "Nessus" || out.Kind != "ports" || len(out.Requirements) != 2 ||
		out.Requirements[0].Fields[0].Text != "TCP 8834" || !strings.Contains(text, "Port: TCP 443") {
		t.Fatalf("ports = %+v\n%s", out, text)
	}
	// The page titled for the kind comes first.
	call(t, cs, "requirements", map[string]any{"product": "Nessus", "kind": "hardware"}, &out)
	if len(out.Requirements) != 2 || !strings.HasSuffix(out.Requirements[0].URL, "HardwareRequirements.htm") {
		t.Errorf("hardware = %+v", out.Requirements)
	}
	call(t, cs, "requirements", map[string]any{"query": "Nessus 10.5 ports"}, &out)
	if len(out.Requirements) != 1 || out.Requirements[0].Version != "10.5" {
		t.Errorf("10.5 = %+v", out.Requirements)
	}
	// Words narrow the rows when some have them, and are dropped when none do.
	call(t, cs, "requirements", map[string]any{"query": "Nessus ports 443"}, &out)
	if len(out.Requirements) != 1 || out.Requirements[0].Fields[0].Text != "TCP 443" {
		t.Errorf("443 = %+v", out.Requirements)
	}
	call(t, cs, "requirements", map[string]any{"query": "Nessus ports for zebras"}, &out)
	if len(out.Requirements) != 2 {
		t.Errorf("zebras = %+v", out.Requirements)
	}
	call(t, cs, "requirements", map[string]any{"product": "Security Center", "kind": "ports"}, &out)
	if len(out.Requirements) != 0 || out.Message == "" {
		t.Errorf("none = %+v", out)
	}
	for _, args := range []map[string]any{{}, {"product": "Nessus", "kind": "food"}, {"product": "Nope"}} {
		if text, isErr := callRaw(t, cs, "requirements", args, nil); !isErr {
			t.Errorf("%v should fail: %s", args, text)
		}
	}
}

func TestReleaseNotesTool(t *testing.T) {
	cs := connect(t, buildIndexFrom(t, specificFixture))
	var out releasesOut
	text := call(t, cs, "release_notes", map[string]any{"product": "Nessus"}, &out)
	if len(out.Entries) != 3 || out.Entries[0].Version != "10.12.6" || out.Entries[0].Date != "2026-10-22" ||
		!strings.Contains(out.Entries[0].Summary, "scheduling") || out.Entries[0].Markdown != "" || !strings.Contains(text, "2026-10-22") {
		t.Fatalf("entries = %+v", out.Entries)
	}
	for args, want := range map[string]int{
		`{"product":"Nessus","since":"2026-10"}`:                   2,
		`{"product":"Nessus","until":"2026-09"}`:                   1,
		`{"product":"Nessus","since":"2026-10-02","until":"2026"}`: 1,
		`{"version":"10.12"}`:                                      2,
		`{"version":"v10.11.2"}`:                                   1,
		`{"query":"dashboard"}`:                                    1,
		`{"product":"Security Center"}`:                            0,
	} {
		call(t, cs, "release_notes", jsonArgs(t, args), &out)
		if len(out.Entries) != want {
			t.Errorf("%s: %d entries, want %d", args, len(out.Entries), want)
		}
	}
	call(t, cs, "release_notes", map[string]any{"product": "Nessus", "full": true, "limit": 1}, &out)
	if len(out.Entries) != 1 || !strings.Contains(out.Entries[0].Markdown, "## Tenable Nessus 10.12.6") {
		t.Errorf("full = %+v", out.Entries)
	}
	for _, args := range []map[string]any{{"since": "last week"}, {"language": "pt"}, {"language": "de"}, {"product": "Nope"}} {
		if text, isErr := callRaw(t, cs, "release_notes", args, nil); !isErr {
			t.Errorf("%v should fail: %s", args, text)
		}
	}
}

func TestDefineTerm(t *testing.T) {
	cs := connect(t, buildIndexFrom(t, specificFixture))
	var out termOut
	call(t, cs, "define_term", map[string]any{"term": "VPR"}, &out)
	if len(out.Definitions) == 0 || out.Definitions[0].Term != "Vulnerability Priority Rating (VPR)" || !strings.HasSuffix(out.Definitions[0].URL, "#g1") {
		t.Errorf("VPR = %+v", out)
	}
	call(t, cs, "define_term", map[string]any{"term": "NVD"}, &out)
	if len(out.Definitions) != 1 || out.Definitions[0].Term != "National Vulnerability Database" {
		t.Errorf("NVD = %+v", out)
	}
	// The company-wide glossary comes before a product's key terms.
	call(t, cs, "define_term", map[string]any{"term": "agent"}, &out)
	if len(out.Definitions) != 2 || !strings.Contains(out.Definitions[0].URL, "/glossary/") {
		t.Errorf("agent = %+v", out.Definitions)
	}
	text := call(t, cs, "define_term", map[string]any{"term": "Tenable.io"}, &out)
	if out.Product == nil || out.Product.Name != "Tenable Vulnerability Management" || !strings.Contains(text, `"Tenable.io" refers to Tenable Vulnerability Management`) {
		t.Errorf("Tenable.io = %+v; %q", out.Product, text)
	}
	text = call(t, cs, "define_term", map[string]any{"term": "Nessus"}, &out)
	if out.Product == nil || !strings.Contains(text, "Tenable Nessus is a Tenable product") {
		t.Errorf("Nessus = %q", text)
	}
	call(t, cs, "define_term", map[string]any{"term": "zebra"}, &out)
	if len(out.Definitions) != 0 || out.Message == "" {
		t.Errorf("zebra = %+v", out)
	}
	call(t, cs, "define_term", map[string]any{"term": "agent", "language": "en"}, &out)
	if len(out.Definitions) != 2 {
		t.Errorf("language en = %+v", out)
	}
	for _, args := range []map[string]any{{"term": ""}, {"term": "x", "language": "pt"}} {
		if text, isErr := callRaw(t, cs, "define_term", args, nil); !isErr {
			t.Errorf("%v should fail: %s", args, text)
		}
	}
}

func jsonArgs(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestNonEnglishSettingsNote(t *testing.T) {
	cs := connect(t, buildIndexFrom(t, specificFixture))
	var out settingsOut
	call(t, cs, "find_setting", map[string]any{"query": "Nessus のスキャン設定 max hosts"}, &out)
	if len(out.Notes) == 0 || out.Notes[len(out.Notes)-1] != extractedNote {
		t.Errorf("notes = %q", out.Notes)
	}
}
