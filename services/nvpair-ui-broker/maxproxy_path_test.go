// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveMaxProxyPath pins the asymmetry that makes an optional worker safe
// to run without.
//
// An explicit --max-proxy-path that does not resolve is an operator mistake and
// must be reported, because silently ignoring it would leave the operator
// believing MAX is running. An absent default sibling is not a mistake at all —
// it is every host that simply does not front a MAX engine — so it reports an
// error the caller degrades on rather than aborting the broker.
func TestResolveMaxProxyPath(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "max-proxy")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := resolveMaxProxyPath(binary)
	if err != nil {
		t.Fatalf("explicit path that exists: %v", err)
	}
	if got != binary {
		t.Fatalf("resolved %q, want %q", got, binary)
	}

	missing := filepath.Join(dir, "not-here")
	if _, err := resolveMaxProxyPath(missing); err == nil {
		t.Fatal("an explicit path that does not exist must be reported, not ignored")
	} else if !strings.Contains(err.Error(), "max-proxy") {
		t.Fatalf("error %q does not name the binary the operator asked for", err)
	}

	// With no override, resolution looks for a sibling in the working
	// directory. The message must name the flag, since that is the operator's
	// way out.
	t.Chdir(t.TempDir())
	if _, err := resolveMaxProxyPath(""); err == nil {
		t.Fatal("expected an error with no sibling binary present")
	} else if !strings.Contains(err.Error(), "--max-proxy-path") {
		t.Fatalf("error %q does not mention the override flag", err)
	}
}
