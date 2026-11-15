// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The server tells clients its icon: a 128-pixel PNG, as a data URI.
func TestServerIcon(t *testing.T) {
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	ss, err := New(Options{}).Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	icons := cs.InitializeResult().ServerInfo.Icons
	if len(icons) != 1 || icons[0].MIMEType != "image/png" || len(icons[0].Sizes) != 1 || icons[0].Sizes[0] != "128x128" {
		t.Fatalf("icons = %+v", icons)
	}
	data, ok := strings.CutPrefix(icons[0].Source, "data:image/png;base64,")
	if !ok {
		t.Fatalf("src = %.40q…", icons[0].Source)
	}
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(raw))
	if err != nil || cfg.Width != 128 || cfg.Height != 128 {
		t.Errorf("the icon is %dx%d (%v)", cfg.Width, cfg.Height, err)
	}
}

// server.json names the 512-pixel icon for the MCP Registry, at the release's tag,
// and the release can fill the tag in: build-release.sh's sed replaces each
// placeholder once a line.
func TestRegistryIcon(t *testing.T) {
	root := filepath.Join("..", "..")
	data, err := os.ReadFile(filepath.Join(root, "server.json"))
	if err != nil {
		t.Fatal(err)
	}
	for i, line := range strings.Split(string(data), "\n") {
		if strings.Count(line, "RELEASE_TAG") > 1 {
			t.Errorf("server.json line %d has RELEASE_TAG twice; the release replaces only the first", i+1)
		}
	}
	var s struct {
		Icons []struct {
			Src, MimeType string
			Sizes         []string
		}
	}
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatal(err)
	}
	const want = "https://raw.githubusercontent.com/lanz/tenable-docs-mcp/RELEASE_TAG/docs/images/icon.png"
	if len(s.Icons) != 1 || s.Icons[0].Src != want || s.Icons[0].MimeType != "image/png" || len(s.Icons[0].Sizes) != 1 || s.Icons[0].Sizes[0] != "512x512" {
		t.Fatalf("icons = %+v", s.Icons)
	}
	f, err := os.Open(filepath.Join(root, "docs", "images", "icon.png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if cfg, err := png.DecodeConfig(f); err != nil || cfg.Width != 512 || cfg.Height != 512 {
		t.Errorf("docs/images/icon.png is %dx%d (%v)", cfg.Width, cfg.Height, err)
	}
}
