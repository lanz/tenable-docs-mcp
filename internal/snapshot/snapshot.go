// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

// Package snapshot writes and reads the official index's content snapshot: the parsed
// pages and tables of contents of each shard, which clients download and index locally.
//
// A snapshot is a folder with one zstd-compressed JSON Lines file per shard and a
// manifest.json that lists them with their sizes and SHA-256 checksums. Each line holds
// a table of contents or a page. A shard's file is named for its shard and the start
// of its checksum, such as en-current-3f2a9c1b0d4e.jsonl.zst, so a new snapshot never
// overwrites an old one's files: publishing uploads the shards first and the manifest
// last, and a client that reads the old manifest meanwhile still finds its files.
package snapshot

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/lanz/tenable-docs-mcp/internal/build"
	"github.com/lanz/tenable-docs-mcp/internal/index"
	"github.com/lanz/tenable-docs-mcp/internal/site"
)

// Format is the snapshot format this version writes and reads.
const Format = 1

// ManifestFile is the manifest's name in a snapshot folder.
const ManifestFile = "manifest.json"

// maxDecoderMemory limits what decompressing one shard can use, against corrupt or
// hostile files.
const maxDecoderMemory = 512 << 20

// Manifest describes a snapshot.
type Manifest struct {
	Format int `json:"format"`
	// BuiltAt is when the snapshot was built (RFC 3339, UTC).
	BuiltAt string `json:"built_at"`
	// Generator is the program and version that built it.
	Generator string  `json:"generator"`
	Shards    []Shard `json:"shards"`
}

// Shard describes one shard's file.
type Shard struct {
	Key    string `json:"key"`
	Site   string `json:"site"`
	Older  bool   `json:"older"`
	File   string `json:"file"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	Pages  int    `json:"pages"`
	Tocs   int    `json:"tocs"`
}

// IndexKey returns the index shard the file holds.
func (s Shard) IndexKey() index.Key { return index.Key{Site: s.Site, Older: s.Older} }

// record is one line of a shard file: a table of contents or a page.
type record struct {
	Toc  *tocRecord  `json:"toc,omitempty"`
	Page *build.Page `json:"page,omitempty"`
}

type tocRecord struct {
	// Guide is the guide's root URL.
	Guide   string           `json:"guide"`
	Entries []*site.TocEntry `json:"entries"`
}

// Writer writes a snapshot. It's a build.Sink.
type Writer struct {
	dir       string
	generator string
	builtAt   time.Time
	files     map[index.Key]*shardWriter
}

type shardWriter struct {
	tmp   string
	f     *os.File
	sum   hash.Hash
	size  countWriter
	zw    *zstd.Encoder
	buf   *bufio.Writer
	enc   *json.Encoder
	pages int
	tocs  int
}

type countWriter struct{ n int64 }

func (c *countWriter) Write(p []byte) (int, error) { c.n += int64(len(p)); return len(p), nil }

// NewWriter starts a snapshot in dir, which it creates.
func NewWriter(dir, generator string, builtAt time.Time) (*Writer, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Writer{dir: dir, generator: generator, builtAt: builtAt.UTC(), files: map[index.Key]*shardWriter{}}, nil
}

// FileName is a shard's file name, for its key and checksum (hex).
func FileName(k index.Key, sha256Hex string) string {
	return k.String() + "-" + sha256Hex[:hashLen] + ".jsonl.zst"
}

// hashLen is how much of the checksum a file name carries.
const hashLen = 12

func (w *Writer) shard(k index.Key) (*shardWriter, error) {
	if sw, ok := w.files[k]; ok {
		return sw, nil
	}
	f, err := os.CreateTemp(w.dir, "."+k.String()+".*.tmp")
	if err != nil {
		return nil, err
	}
	sw := &shardWriter{tmp: f.Name(), f: f, sum: sha256.New()}
	zw, err := zstd.NewWriter(io.MultiWriter(f, sw.sum, &sw.size), zstd.WithEncoderLevel(zstd.SpeedBetterCompression))
	if err != nil {
		f.Close()
		os.Remove(sw.tmp)
		return nil, err
	}
	sw.zw = zw
	sw.buf = bufio.NewWriterSize(zw, 1<<20)
	sw.enc = json.NewEncoder(sw.buf)
	sw.enc.SetEscapeHTML(false)
	w.files[k] = sw
	return sw, nil
}

// Page writes a page to its shard's file.
func (w *Writer) Page(_ context.Context, k index.Key, p build.Page) error {
	sw, err := w.shard(k)
	if err != nil {
		return err
	}
	sw.pages++
	return sw.enc.Encode(record{Page: &p})
}

// Toc writes a guide's table of contents to its shard's file.
func (w *Writer) Toc(_ context.Context, k index.Key, g site.Guide, toc []*site.TocEntry) error {
	sw, err := w.shard(k)
	if err != nil {
		return err
	}
	sw.tocs++
	return sw.enc.Encode(record{Toc: &tocRecord{Guide: g.RootURL, Entries: toc}})
}

// Close finishes each shard's file and writes the manifest, last, so a reader never
// sees a manifest for files that aren't there.
func (w *Writer) Close() (Manifest, error) {
	m := Manifest{Format: Format, BuiltAt: w.builtAt.Format(time.RFC3339), Generator: w.generator}
	keys := make([]index.Key, 0, len(w.files))
	for k := range w.files {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
	for _, k := range keys {
		sw := w.files[k]
		err := errors.Join(sw.buf.Flush(), sw.zw.Close(), sw.f.Sync(), sw.f.Close())
		sum := hex.EncodeToString(sw.sum.Sum(nil))
		if err == nil {
			err = os.Rename(sw.tmp, filepath.Join(w.dir, FileName(k, sum)))
		}
		if err != nil {
			w.Abort()
			return Manifest{}, err
		}
		delete(w.files, k)
		m.Shards = append(m.Shards, Shard{Key: k.String(), Site: k.Site, Older: k.Older, File: FileName(k, sum),
			Size: sw.size.n, SHA256: sum, Pages: sw.pages, Tocs: sw.tocs})
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return Manifest{}, err
	}
	if err := writeFileAtomic(filepath.Join(w.dir, ManifestFile), append(data, '\n')); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// Abort removes the files of shards that weren't finished.
func (w *Writer) Abort() {
	for k, sw := range w.files {
		sw.f.Close()
		os.Remove(sw.tmp)
		delete(w.files, k)
	}
}

func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ReadManifest reads and checks a snapshot's manifest.
func ReadManifest(dir string) (Manifest, error) {
	data, err := os.ReadFile(filepath.Join(dir, ManifestFile))
	if err != nil {
		return Manifest{}, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return Manifest{}, fmt.Errorf("%s: %w", ManifestFile, err)
	}
	if m.Format != Format {
		return Manifest{}, fmt.Errorf("the snapshot is format %d, and this version of tenable-docs-mcp reads format %d; update it", m.Format, Format)
	}
	for _, s := range m.Shards {
		if _, ok := site.ByCode(s.Site); !ok || len(s.SHA256) != 64 || s.Key != s.IndexKey().String() ||
			s.File != FileName(s.IndexKey(), strings.ToLower(s.SHA256)) {
			return Manifest{}, fmt.Errorf("%s: unexpected shard %q (%s)", ManifestFile, s.Key, s.File)
		}
	}
	return m, nil
}

// Verify checks a shard's file against its size and checksum.
func Verify(dir string, s Shard) error {
	f, err := os.Open(filepath.Join(dir, s.File))
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return err
	}
	if n != s.Size {
		return fmt.Errorf("%s is %d bytes; the manifest says %d", s.File, n, s.Size)
	}
	if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, s.SHA256) {
		return fmt.Errorf("%s doesn't match its checksum in the manifest", s.File)
	}
	return nil
}

// Read verifies a shard's file and gives its tables of contents and pages to sink.
func Read(ctx context.Context, dir string, s Shard, sink build.Sink) error {
	if err := Verify(dir, s); err != nil {
		return err
	}
	f, err := os.Open(filepath.Join(dir, s.File))
	if err != nil {
		return err
	}
	defer f.Close()
	zr, err := zstd.NewReader(f, zstd.WithDecoderMaxMemory(maxDecoderMemory), zstd.WithDecoderConcurrency(1))
	if err != nil {
		return err
	}
	defer zr.Close()
	k := s.IndexKey()
	dec := json.NewDecoder(bufio.NewReaderSize(zr, 1<<20))
	pages := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		var r record
		if err := dec.Decode(&r); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return fmt.Errorf("%s: %w", s.File, err)
		}
		switch {
		case r.Toc != nil:
			g, ok := site.GuideOf(strings.TrimRight(r.Toc.Guide, "/") + "/Content/x.htm")
			if !ok || g.RootURL != r.Toc.Guide {
				return fmt.Errorf("%s: a table of contents for %q, which isn't a guide", s.File, r.Toc.Guide)
			}
			if err := sink.Toc(ctx, k, g, r.Toc.Entries); err != nil {
				return err
			}
		case r.Page != nil && r.Page.Page != nil:
			if key, ok := index.KeyFor(r.Page.Page.URL); !ok || key != k {
				return fmt.Errorf("%s: page %s doesn't belong in %s", s.File, r.Page.Page.URL, k)
			}
			if err := sink.Page(ctx, k, *r.Page); err != nil {
				return err
			}
			pages++
		default:
			return fmt.Errorf("%s: a line that's neither a page nor a table of contents", s.File)
		}
	}
	if pages != s.Pages {
		return fmt.Errorf("%s has %d pages; the manifest says %d", s.File, pages, s.Pages)
	}
	return nil
}
