// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestQaicRefreshReplacesAnOlderLauncher(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "qaic-serve")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho old\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := refreshQaicLauncher(dir); err != nil {
		t.Fatalf("refreshQaicLauncher() error = %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, qaicLauncher) {
		t.Fatalf("launcher was not replaced with the current one (%v)", err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o755 {
		t.Fatalf("launcher mode = %v, want 0755", info.Mode().Perm())
	}
}

func TestQaicRefreshLeavesACurrentLauncherAlone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "qaic-serve")
	if err := os.WriteFile(path, qaicLauncher, 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}

	if err := refreshQaicLauncher(dir); err != nil {
		t.Fatalf("refreshQaicLauncher() error = %v", err)
	}
	if info, _ := os.Stat(path); !info.ModTime().Equal(old) {
		t.Fatal("a current launcher was rewritten")
	}
}

func TestQaicRefreshDoesNotInstall(t *testing.T) {
	dir := t.TempDir()
	if err := refreshQaicLauncher(dir); err != nil {
		t.Fatalf("refreshQaicLauncher() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "qaic-serve")); !os.IsNotExist(err) {
		t.Fatal("a launcher was written where the engine is not installed")
	}
}
