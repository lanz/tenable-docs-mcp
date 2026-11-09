// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

// Command mcpb writes the manifest.json of a Claude Desktop extension (an MCP Bundle,
// .mcpb), with the server's tools as the server itself lists them. The bundle holds a
// binary for each platform: server/darwin (universal, for Apple silicon and Intel),
// server/win32 (amd64, which Windows on Arm runs too), and server/linux (amd64).
//
//	go run ./scripts/mcpb -version v1.2.3 -out dir/manifest.json
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lanz/tenable-docs-mcp/internal/server"
)

func main() {
	version := flag.String("version", "", "the release version, such as v1.2.3")
	out := flag.String("out", "manifest.json", "where to write the manifest")
	flag.Parse()
	if *version == "" {
		fmt.Fprintln(os.Stderr, "usage: mcpb -version v1.2.3 [-out manifest.json]")
		os.Exit(2)
	}
	m, err := manifest(strings.TrimPrefix(*version, "v"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "mcpb:", err)
		os.Exit(1)
	}
	data, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(*out, append(data, '\n'), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "mcpb:", err)
		os.Exit(1)
	}
}

type tool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Bin is the path in the bundle of a platform's binary.
func Bin(platform string) string {
	if platform == "win32" {
		return "server/win32/" + server.Name + ".exe"
	}
	return "server/" + platform + "/" + server.Name
}

func manifest(version string) (map[string]any, error) {
	tools, err := serverTools()
	if err != nil {
		return nil, err
	}
	command := func(platform string) map[string]any {
		return map[string]any{"command": "${__dirname}/" + Bin(platform)}
	}
	return map[string]any{
		"manifest_version": "0.3",
		"name":             server.Name,
		"display_name":     "Tenable Docs MCP",
		"version":          version,
		"description":      "Answers from Tenable's product documentation, with a link to the exact section.",
		"long_description": "Searches docs.tenable.com and its seven translated sites, in every product, guide, and " +
			"published version, from a local index that downloads on first run and updates daily. Answers how-to " +
			"questions, settings, required roles, system requirements, and release notes, compares versions, and " +
			"checks an answer's docs links. A community project; not an official Tenable product.",
		"author":        map[string]string{"name": "Lance Duvall", "url": "https://github.com/lanz"},
		"repository":    map[string]string{"type": "git", "url": server.ProjectURL},
		"homepage":      server.ProjectURL,
		"documentation": server.ProjectURL + "#readme",
		"support":       server.ProjectURL + "/issues",
		"license":       "GPL-3.0-or-later",
		"keywords":      []string{"tenable", "documentation", "docs", "nessus", "vulnerability management", "security center", "search"},
		// Feedback and gap reports the user chooses to send go to Tenable.
		"privacy_policies": []string{"https://www.tenable.com/privacy-policy"},
		"server": map[string]any{
			"type":        "binary",
			"entry_point": Bin("darwin"),
			"mcp_config": map[string]any{
				"command": "${__dirname}/" + Bin("darwin"),
				"args":    []string{"serve"},
				"platform_overrides": map[string]any{
					"win32": command("win32"),
					"linux": command("linux"),
				},
			},
		},
		"tools": tools,
		// The prompts' text depends on their arguments, so the server makes it.
		"prompts_generated": true,
		"compatibility":     map[string]any{"platforms": []string{"darwin", "win32", "linux"}},
	}, nil
}

// serverTools lists the server's tools, each with the first sentence of its
// description.
func serverTools() ([]tool, error) {
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	ss, err := server.New(server.Options{}).Connect(ctx, st, nil)
	if err != nil {
		return nil, err
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "mcpb", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		return nil, err
	}
	defer cs.Close()
	res, err := cs.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		return nil, err
	}
	var out []tool
	for _, t := range res.Tools {
		out = append(out, tool{Name: t.Name, Description: firstSentence(t.Description)})
	}
	return out, nil
}

func firstSentence(s string) string {
	if i := strings.Index(s, ". "); i >= 0 {
		return s[:i+1]
	}
	return s
}
