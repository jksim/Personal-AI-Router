// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/base64"
	"os"
	"testing"
)

func qaicInstallRun(t *testing.T) []string {
	t.Helper()
	reg := NewRegistry()
	if err := reg.LoadFS(bundledManifests, "manifests"); err != nil {
		t.Fatalf("load bundled manifests: %v", err)
	}
	m, ok := reg.Get("qaic")
	if !ok {
		t.Fatal("bundled qaic manifest missing")
	}
	return m.Platforms["linux/amd64"].Install.Run
}

// TestQaicManifestCarriesTheLauncher keeps the base64 launcher in qaic.json
// identical to launchers/qaic-serve.sh, the copy people read and review.
func TestQaicManifestCarriesTheLauncher(t *testing.T) {
	run := qaicInstallRun(t)
	encoded, err := base64.StdEncoding.DecodeString(run[len(run)-1])
	if err != nil {
		t.Fatalf("decode the launcher: %v", err)
	}
	source, err := os.ReadFile("launchers/qaic-serve.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, source) {
		t.Fatal("qaic.json's launcher differs from launchers/qaic-serve.sh; regenerate the base64")
	}
}

// TestQaicInstallUsesOnlyPositionalParameters guards against the engine
// manager expanding a named $VAR in an install argument to its own empty
// environment value.
func TestQaicInstallUsesOnlyPositionalParameters(t *testing.T) {
	for _, arg := range qaicInstallRun(t) {
		if unixEnvRe.MatchString(arg) {
			t.Fatalf("install argument %q has a named variable the engine manager would expand", arg)
		}
	}
}
