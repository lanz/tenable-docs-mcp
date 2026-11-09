// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"debug/macho"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestJoin builds a tiny program for both Mac architectures, joins them, and reads the
// result back with the standard library's reader of universal binaries.
func TestJoin(t *testing.T) {
	if testing.Short() {
		t.Skip("builds programs")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	if err := os.WriteFile(src, []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var ins []string
	for _, arch := range []string{"amd64", "arm64"} {
		out := filepath.Join(dir, arch)
		cmd := exec.Command("go", "build", "-o", out, src)
		cmd.Env = append(os.Environ(), "GOOS=darwin", "GOARCH="+arch, "CGO_ENABLED=0")
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v\n%s", arch, err, b)
		}
		ins = append(ins, out)
	}
	fat := filepath.Join(dir, "fat")
	if err := join(fat, ins); err != nil {
		t.Fatal(err)
	}
	f, err := macho.OpenFat(fat)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cpus := map[macho.Cpu]bool{}
	for _, a := range f.Arches {
		cpus[a.Cpu] = true
		if a.Offset%(1<<alignment) != 0 {
			t.Errorf("%v starts at %d, not aligned", a.Cpu, a.Offset)
		}
		// Each architecture's bytes are its original binary.
		orig, _ := os.ReadFile(ins[map[macho.Cpu]int{macho.CpuAmd64: 0, macho.CpuArm64: 1}[a.Cpu]])
		data, _ := os.ReadFile(fat)
		if !bytes.Equal(data[a.Offset:a.Offset+a.Size], orig) {
			t.Errorf("%v's bytes differ from its binary", a.Cpu)
		}
	}
	if len(f.Arches) != 2 || !cpus[macho.CpuAmd64] || !cpus[macho.CpuArm64] {
		t.Errorf("arches = %+v", f.Arches)
	}
}

// TestJoinRuns runs the universal binary, where it can run.
func TestJoinRuns(t *testing.T) {
	if runtime.GOOS != "darwin" || testing.Short() {
		t.Skip("runs only on macOS")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	if err := os.WriteFile(src, []byte("package main\nimport \"fmt\"\nfunc main() { fmt.Print(\"ok\") }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var ins []string
	for _, arch := range []string{"amd64", "arm64"} {
		out := filepath.Join(dir, arch)
		cmd := exec.Command("go", "build", "-o", out, src)
		cmd.Env = append(os.Environ(), "GOOS=darwin", "GOARCH="+arch, "CGO_ENABLED=0")
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v\n%s", arch, err, b)
		}
		ins = append(ins, out)
	}
	fat := filepath.Join(dir, "fat")
	if err := join(fat, ins); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(fat).CombinedOutput()
	if err != nil || string(out) != "ok" {
		t.Errorf("running the universal binary: %v %q", err, out)
	}
}
