// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

// Command fatmacho joins macOS binaries for different architectures into one universal
// ("fat") binary, which runs natively on both Apple silicon and Intel Macs.
//
//	go run ./scripts/fatmacho out in-amd64 in-arm64
package main

import (
	"bytes"
	"debug/macho"
	"encoding/binary"
	"fmt"
	"os"
)

// alignment is the power of two each architecture's binary starts on: 16 KiB, the
// arm64 page size, which suits amd64 too.
const alignment = 14

func main() {
	if len(os.Args) < 4 {
		fmt.Fprintln(os.Stderr, "usage: fatmacho out in1 in2 ...")
		os.Exit(2)
	}
	if err := join(os.Args[1], os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, "fatmacho:", err)
		os.Exit(1)
	}
}

func join(out string, ins []string) error {
	type arch struct {
		cpu, sub uint32
		data     []byte
	}
	var archs []arch
	for _, in := range ins {
		data, err := os.ReadFile(in)
		if err != nil {
			return err
		}
		f, err := macho.NewFile(bytes.NewReader(data))
		if err != nil {
			return fmt.Errorf("%s: %w", in, err)
		}
		archs = append(archs, arch{cpu: uint32(f.Cpu), sub: f.SubCpu, data: data})
	}
	// The header: magic and count, then each architecture's cputype, cpusubtype,
	// offset, size, and alignment, all big-endian.
	var b bytes.Buffer
	be := binary.BigEndian
	put := func(v uint32) { _ = binary.Write(&b, be, v) }
	put(macho.MagicFat)
	put(uint32(len(archs)))
	offset := uint32(8 + 20*len(archs))
	offsets := make([]uint32, len(archs))
	for i, a := range archs {
		offset = alignUp(offset, 1<<alignment)
		offsets[i] = offset
		put(a.cpu)
		put(a.sub)
		put(offset)
		put(uint32(len(a.data)))
		put(alignment)
		offset += uint32(len(a.data))
	}
	for i, a := range archs {
		b.Write(make([]byte, int(offsets[i])-b.Len()))
		b.Write(a.data)
	}
	return os.WriteFile(out, b.Bytes(), 0o755)
}

func alignUp(n, a uint32) uint32 { return (n + a - 1) &^ (a - 1) }
