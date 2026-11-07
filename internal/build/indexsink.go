// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package build

import (
	"context"
	"fmt"
	"sort"

	"github.com/lanz/tenable-docs-mcp/internal/index"
	"github.com/lanz/tenable-docs-mcp/internal/site"
)

// batchSize is how many pages go into the index in one transaction.
const batchSize = 500

// IndexSink writes pages into an index. Each shard is built aside and replaces the
// index's copy only when Finish succeeds, so searches keep working during a build.
type IndexSink struct {
	ix      *index.Index
	builtAt string
	builds  map[index.Key]*index.Shard
	batch   map[index.Key][]index.Doc
}

// NewIndexSink returns a sink that builds shards for ix, marked as built at builtAt.
func NewIndexSink(ix *index.Index, builtAt string) *IndexSink {
	return &IndexSink{ix: ix, builtAt: builtAt, builds: map[index.Key]*index.Shard{}, batch: map[index.Key][]index.Doc{}}
}

func (s *IndexSink) shard(k index.Key) (*index.Shard, error) {
	if b, ok := s.builds[k]; ok {
		return b, nil
	}
	b, err := s.ix.Build(k)
	if err != nil {
		return nil, err
	}
	s.builds[k] = b
	return b, nil
}

// Page adds a page to its shard.
func (s *IndexSink) Page(ctx context.Context, k index.Key, p Page) error {
	st, ok := site.ByCode(k.Site)
	if !ok {
		return fmt.Errorf("no site %q", k.Site)
	}
	if _, err := s.shard(k); err != nil {
		return err
	}
	d := index.NewDoc(p.Page, st, p.Entry, p.Trail)
	d.LastModified = p.LastModified
	s.batch[k] = append(s.batch[k], d)
	if len(s.batch[k]) >= batchSize {
		return s.flush(ctx, k)
	}
	return nil
}

// Toc stores a guide's table of contents in its shard.
func (s *IndexSink) Toc(ctx context.Context, k index.Key, g site.Guide, toc []*site.TocEntry) error {
	b, err := s.shard(k)
	if err != nil {
		return err
	}
	return b.Update(ctx, func(tx *index.Tx) error { return tx.PutToc(g, toc) })
}

func (s *IndexSink) flush(ctx context.Context, k index.Key) error {
	docs := s.batch[k]
	if len(docs) == 0 {
		return nil
	}
	s.batch[k] = nil
	return s.builds[k].Update(ctx, func(tx *index.Tx) error {
		for _, d := range docs {
			if _, err := tx.Put(d); err != nil {
				return fmt.Errorf("%s: %w", d.Page.URL, err)
			}
		}
		return nil
	})
}

// Keys lists the shards the sink has built, in order.
func (s *IndexSink) Keys() []index.Key {
	keys := make([]index.Key, 0, len(s.builds))
	for k := range s.builds {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
	return keys
}

// Finish writes what's left, optimizes each shard, and installs them in the index.
func (s *IndexSink) Finish(ctx context.Context) error {
	for _, k := range s.Keys() {
		if err := s.flush(ctx, k); err != nil {
			return err
		}
		b := s.builds[k]
		if err := b.Update(ctx, func(tx *index.Tx) error { return tx.SetMeta("built_at", s.builtAt) }); err != nil {
			return err
		}
		if err := b.Optimize(ctx); err != nil {
			return err
		}
		if err := s.ix.Install(b); err != nil {
			return err
		}
		delete(s.builds, k)
	}
	return nil
}

// Discard throws away shards that weren't finished, leaving the index as it was.
func (s *IndexSink) Discard() {
	for k, b := range s.builds {
		_ = s.ix.Discard(b)
		delete(s.builds, k)
	}
}
