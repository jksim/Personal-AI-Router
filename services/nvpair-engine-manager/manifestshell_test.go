// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"
)

// TestManifestStringsCannotUseShellVariables documents a footgun that cost a
// failed live install before it was understood.
//
// Every resolved manifest argument goes through expandPath, which runs
// os.ExpandEnv. A shell positional like $0 or a shell variable like $D is
// therefore replaced with the empty string before the shell ever sees it — so
// `sh -c 'mkdir "$0/uv"' "{install_dir}"` does not create a directory under the
// install dir, it tries to create /uv. A manifest must interpolate {install_dir}
// literally at every use instead.
func TestManifestStringsCannotUseShellVariables(t *testing.T) {
	if got := expandPathForOS(`"$0/uv"`, "linux"); got != `"/uv"` {
		t.Fatalf("expandPath($0) = %q; the positional survived, so this test's premise is stale", got)
	}
	if got := expandPathForOS(`$D/venv`, "linux"); got != "/venv" {
		t.Fatalf("expandPath($D) = %q, want the variable stripped", got)
	}
}

// TestBundledInstallCommandsAvoidShellVariables enforces the rule above on the
// manifests that actually ship. A shell variable here fails only at install
// time, on a user's machine, with a path error that names neither the manifest
// nor the placeholder.
func TestBundledInstallCommandsAvoidShellVariables(t *testing.T) {
	reg := NewRegistry()
	if err := reg.LoadFS(bundledManifests, "manifests"); err != nil {
		t.Fatalf("bundled manifests invalid: %v", err)
	}
	for _, name := range reg.Names() {
		m, ok := reg.Get(name)
		if !ok {
			continue
		}
		for key, p := range m.Platforms {
			if p.Install == nil {
				continue
			}
			for _, arg := range append(append([]string{}, p.Install.Run...), p.Install.Script...) {
				// $HOME and %USERPROFILE% are the documented exception: those
				// are expanded by expandPath on purpose, which is exactly why
				// every other $-form is not.
				stripped := strings.ReplaceAll(arg, "$HOME", "")
				if idx := strings.Index(stripped, "$"); idx >= 0 {
					t.Errorf("bundled %q platform %q install arg uses a shell variable that expandPath will strip: %s",
						name, key, arg)
				}
			}
		}
	}
}
