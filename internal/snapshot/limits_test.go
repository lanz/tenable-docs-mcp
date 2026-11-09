// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package snapshot

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"

	"github.com/lanz/tenable-docs-mcp/internal/index"
)

// A checksum that isn't hex could make a file name that leaves the snapshot folder,
// and a manifest that lists a shard twice is wrong.
func TestManifestChecksumsAndKeys(t *testing.T) {
	dir, m := writeSnapshot(t)
	write := func(m Manifest) {
		data, _ := json.Marshal(m)
		if err := os.WriteFile(filepath.Join(dir, ManifestFile), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	k := m.Shards[0].IndexKey()
	for name, sum := range map[string]string{
		"a path":    "/../../../.." + strings.Repeat("a", 52),
		"backslash": `\..\..\` + strings.Repeat("a", 57),
		"not hex":   strings.Repeat("g", 64),
	} {
		bad := m
		bad.Shards = append([]Shard(nil), m.Shards...)
		bad.Shards[0].SHA256 = sum
		bad.Shards[0].File = FileName(k, sum)
		write(bad)
		if _, err := ReadManifest(dir); err == nil {
			t.Errorf("%s (%s) should fail", name, bad.Shards[0].File)
		}
	}
	// Uppercase hex is still a checksum.
	upper := m
	upper.Shards = append([]Shard(nil), m.Shards...)
	upper.Shards[0].SHA256 = strings.ToUpper(m.Shards[0].SHA256)
	write(upper)
	if _, err := ReadManifest(dir); err != nil {
		t.Errorf("uppercase checksum: %v", err)
	}
	twice := m
	twice.Shards = append(append([]Shard(nil), m.Shards...), m.Shards[0])
	write(twice)
	if _, err := ReadManifest(dir); err == nil {
		t.Error("a shard listed twice should fail")
	}
}

// shardFile writes a zstd shard file of the given lines for key, and returns its
// manifest entry, so a test can make a file that matches its checksum.
func shardFile(t *testing.T, dir string, k index.Key, lines ...[]byte) Shard {
	t.Helper()
	var buf bytes.Buffer
	zw, err := zstd.NewWriter(&buf)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range lines {
		zw.Write(append(l, '\n'))
	}
	zw.Close()
	sum := sha256.Sum256(buf.Bytes())
	hexSum := hex.EncodeToString(sum[:])
	s := Shard{Key: k.String(), Site: k.Site, Older: k.Older, File: FileName(k, hexSum), Size: int64(buf.Len()), SHA256: hexSum}
	if err := os.WriteFile(filepath.Join(dir, s.File), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return s
}

// A file that matches its checksum still can't hold an oversized record, or
// decompress to more than the limit.
func TestReadLimits(t *testing.T) {
	oldRecord, oldTotal := maxRecord, maxDecompressed
	t.Cleanup(func() { maxRecord, maxDecompressed = oldRecord, oldTotal })
	maxRecord, maxDecompressed = 4096, 64<<10
	dir := t.TempDir()
	k := index.Key{Site: "en"}

	long := []byte(`{"toc":{"guide":"` + strings.Repeat("x", 8192) + `"}}`)
	if err := Read(ctx, dir, shardFile(t, dir, k, long), discard{}); err == nil || !strings.Contains(err.Error(), "a record longer than") {
		t.Errorf("a long record = %v", err)
	}
	// Empty lines are skipped, so only the total stops these.
	many := make([][]byte, 70_000)
	if err := Read(ctx, dir, shardFile(t, dir, k, many...), discard{}); err == nil || !strings.Contains(err.Error(), "decompresses to more than") {
		t.Errorf("too much in all = %v", err)
	}
	// An error quoting a value from the file keeps it short.
	bad := []byte(`{"toc":{"guide":"https://example.com/` + strings.Repeat("y", 2000) + `/"}}`)
	if err := Read(ctx, dir, shardFile(t, dir, k, bad), discard{}); err == nil || len(err.Error()) > 400 {
		t.Errorf("a long value in an error = %v", err)
	}
}
