// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestProcessWorkDir pins where a supervised engine runs.
//
// An inherited working directory is configuration the user never agreed to.
// MAX reads a .env from its working directory, and settings found there
// override the ones the manifest sets — including the loopback bind, whose
// absence would publish the engine on the LAN. Running in NVPAIR's own install
// dir puts the engine somewhere NVPAIR controls.
func TestProcessWorkDir(t *testing.T) {
	dir := t.TempDir()
	if got := processWorkDir(dir); got != dir {
		t.Fatalf("processWorkDir(%q) = %q, want the install dir", dir, got)
	}

	// An adopted engine — already on PATH, never installed by NVPAIR — has no
	// install dir. Falling back beats refusing to start it.
	if got := processWorkDir(""); got != "" {
		t.Fatalf("processWorkDir(\"\") = %q, want the inherited directory", got)
	}
	missing := filepath.Join(dir, "not-created")
	if got := processWorkDir(missing); got != "" {
		t.Fatalf("processWorkDir(missing) = %q, want the inherited directory", got)
	}

	// A file is not a working directory; cmd.Start would fail with a confusing
	// error rather than a clear one.
	file := filepath.Join(dir, "a-file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := processWorkDir(file); got != "" {
		t.Fatalf("processWorkDir(file) = %q, want the inherited directory", got)
	}
}

// TestStartManagedProcRunsInTheGivenDirectory proves the directory is actually
// applied, not merely computed.
func TestStartManagedProcRunsInTheGivenDirectory(t *testing.T) {
	dir := t.TempDir()
	lines := make(chan string, 8)
	proc, err := startManagedProc(fakeEngineBin, []string{"pwd"}, nil, dir, func(_, line string) {
		select {
		case lines <- line:
		default:
		}
	})
	if err != nil {
		t.Fatalf("startManagedProc: %v", err)
	}
	<-proc.done

	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	for {
		select {
		case line := <-lines:
			if line == dir || line == resolved {
				return
			}
		default:
			t.Fatalf("engine did not report running in %q", dir)
		}
	}
}
