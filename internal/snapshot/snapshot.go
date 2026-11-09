// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

// Package snapshot writes and reads the published index's content snapshot: the parsed
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
	"regexp"
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

// Limits on reading a shard file, against corrupt or hostile ones: what the zstd
// decoder may use for its window, how large one record (one line) may be, and how much
// the whole file may decompress to. The largest real record is under 1 MiB and the
// largest shard decompresses to under 100 MiB.
const maxDecoderMemory = 512 << 20

// Variables, so tests can lower them.
var (
	maxRecord       = 16 << 20
	maxDecompressed = int64(1 << 30)
)

// sha256Hex matches a checksum as the manifest gives it, in lowercase.
var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// clip shortens a value from a snapshot for an error message, which can end up in the
// state file and in front of an assistant.
func clip(s string) string {
	if r := []rune(s); len(r) > 200 {
		return string(r[:200]) + "…"
	}
	return s
}

// errTooBig is a shard file that decompresses to more than maxDecompressed.
var errTooBig = errors.New("too large")

// capReader reads at most n bytes from r, and fails with errTooBig after that, so a
// file over the limit fails as a whole rather than at a line it cut short.
type capReader struct {
	r io.Reader
	n int64
}

func (c *capReader) Read(p []byte) (int, error) {
	if c.n < 0 {
		return 0, errTooBig
	}
	if int64(len(p)) > c.n+1 {
		p = p[:c.n+1]
	}
	k, err := c.r.Read(p)
	c.n -= int64(k)
	if c.n < 0 {
		return 0, errTooBig
	}
	return k, err
}

// mib describes a size in MiB, or in KiB when it's smaller (as in tests).
func mib(n int64) string {
	if n < 1<<20 {
		return fmt.Sprintf("%d KiB", n>>10)
	}
	return fmt.Sprintf("%d MiB", n>>20)
}

// Manifest describes a snapshot.
type Manifest struct {
	Format int `json:"format"`
	// BuiltAt is when the snapshot was built (RFC 3339, UTC).
	BuiltAt string `json:"built_at"`
	// Generator is the program and version that built it.
	Generator string `json:"generator"`
	// LatestRelease is the newest release of the server (v1.2.3), so servers that
	// download the snapshot can tell their users when a newer one is available. A
	// snapshot built by a release says which it is; one built by a prerelease or a
	// development build leaves it out. Servers that don't know the field ignore it.
	LatestRelease string  `json:"latest_release,omitempty"`
	Shards        []Shard `json:"shards"`
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
	Name    string           `json:"name,omitempty"`
	Entries []*site.TocEntry `json:"entries"`
}

// Writer writes a snapshot. It's a build.Sink.
type Writer struct {
	// LatestRelease, when set, goes in the manifest's latest_release.
	LatestRelease string

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
	return sw.enc.Encode(record{Toc: &tocRecord{Guide: g.RootURL, Name: g.Name, Entries: toc}})
}

// Close finishes each shard's file and writes the manifest, last, so a reader never
// sees a manifest for files that aren't there.
func (w *Writer) Close() (Manifest, error) {
	m := Manifest{Format: Format, BuiltAt: w.builtAt.Format(time.RFC3339), Generator: w.generator, LatestRelease: w.LatestRelease}
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
	return ParseManifest(data)
}

// ErrFormat is returned for a manifest in a format this version doesn't read.
var ErrFormat = errors.New("unsupported snapshot format")

// ParseManifest checks a manifest: its format, and that each shard's site is known
// and its file is named for its key and checksum.
func ParseManifest(data []byte) (Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return Manifest{}, fmt.Errorf("%s: %w", ManifestFile, err)
	}
	if m.Format != Format {
		return Manifest{}, fmt.Errorf("%w: the snapshot is format %d, and this version of tenable-docs-mcp reads format %d; update it", ErrFormat, m.Format, Format)
	}
	seen := map[string]bool{}
	for _, s := range m.Shards {
		// The checksum must be hex, so the file name it makes can't hold a path.
		if _, ok := site.ByCode(s.Site); !ok || !sha256Hex.MatchString(strings.ToLower(s.SHA256)) ||
			s.Key != s.IndexKey().String() || s.File != FileName(s.IndexKey(), strings.ToLower(s.SHA256)) ||
			filepath.Base(s.File) != s.File || seen[s.Key] {
			return Manifest{}, fmt.Errorf("%s: unexpected shard %q (%s)", ManifestFile, clip(s.Key), clip(s.File))
		}
		seen[s.Key] = true
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
	// Each line is one record. Both a line's size and the whole file's decompressed size
	// are limited, so a file that matches its checksum can't still exhaust memory.
	// The scanner's limit is the larger of maxRecord and its first buffer's capacity.
	sc := bufio.NewScanner(&capReader{r: zr, n: maxDecompressed})
	sc.Buffer(make([]byte, 0, min(1<<20, maxRecord)), maxRecord)
	pages := 0
	for sc.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var r record
		if err := json.Unmarshal(line, &r); err != nil {
			return fmt.Errorf("%s: %w", s.File, err)
		}
		switch {
		case r.Toc != nil:
			g, ok := site.GuideOf(strings.TrimRight(r.Toc.Guide, "/") + "/Content/x.htm")
			if !ok || g.RootURL != r.Toc.Guide {
				return fmt.Errorf("%s: a table of contents for %q, which isn't a guide", s.File, clip(r.Toc.Guide))
			}
			g.Name = r.Toc.Name
			if err := sink.Toc(ctx, k, g, r.Toc.Entries); err != nil {
				return err
			}
		case r.Page != nil && r.Page.Page != nil:
			if key, ok := index.KeyFor(r.Page.Page.URL); !ok || key != k {
				return fmt.Errorf("%s: page %s doesn't belong in %s", s.File, clip(r.Page.Page.URL), k)
			}
			if err := sink.Page(ctx, k, *r.Page); err != nil {
				return err
			}
			pages++
		default:
			return fmt.Errorf("%s: a line that's neither a page nor a table of contents", s.File)
		}
	}
	switch err := sc.Err(); {
	case errors.Is(err, bufio.ErrTooLong):
		return fmt.Errorf("%s: a record longer than %s", s.File, mib(int64(maxRecord)))
	case errors.Is(err, errTooBig):
		return fmt.Errorf("%s decompresses to more than %s", s.File, mib(maxDecompressed))
	case err != nil:
		return fmt.Errorf("%s: %w", s.File, err)
	}
	if pages != s.Pages {
		return fmt.Errorf("%s has %d pages; the manifest says %d", s.File, pages, s.Pages)
	}
	return nil
}
