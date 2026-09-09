// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// TestEngineModelsDirIsPerEngine pins the fix for a value that used to be
// LM Studio's for every engine.
//
// That was harmless only while LM Studio was the sole engine with a path-based
// model action. With a second one it is a real defect: a remove_path rooted at
// the wrong tree either finds nothing or confines a delete somewhere it should
// not be looking.
func TestEngineModelsDirIsPerEngine(t *testing.T) {
	lmstudio := engineModelsDir("lmstudio")
	if !strings.Contains(lmstudio, ".lmstudio") {
		t.Fatalf("lmstudio models dir = %q, want LM Studio's own tree", lmstudio)
	}
	max := engineModelsDir("max")
	if !strings.Contains(max, "huggingface") {
		t.Fatalf("max models dir = %q, want the HuggingFace cache", max)
	}
	if max == lmstudio {
		t.Fatal("max and lmstudio resolve to the same models dir")
	}
	// An engine with no path-based model action keeps the historical value, so
	// this change cannot alter any existing engine's behaviour.
	if got := engineModelsDir("ollama"); got != lmstudio {
		t.Fatalf("an unlisted engine's models dir = %q, want the unchanged default %q", got, lmstudio)
	}
}

// TestHFCacheDirHonoursTheEnvironment covers the override precedence the
// HuggingFace libraries themselves use. A user who moved their cache must not
// be told their models are missing.
func TestHFCacheDirHonoursTheEnvironment(t *testing.T) {
	t.Run("HF_HOME wins", func(t *testing.T) {
		t.Setenv("HF_HOME", "/tmp/hf-home")
		t.Setenv("HF_HUB_CACHE", "")
		if got, want := hfCacheDir(), filepath.Join("/tmp/hf-home", "hub"); got != want {
			t.Fatalf("hfCacheDir() = %q, want %q", got, want)
		}
	})
	t.Run("HF_HUB_CACHE is used when HF_HOME is unset", func(t *testing.T) {
		t.Setenv("HF_HOME", "")
		t.Setenv("HF_HUB_CACHE", "/tmp/hub-cache")
		if got := hfCacheDir(); got != "/tmp/hub-cache" {
			t.Fatalf("hfCacheDir() = %q, want /tmp/hub-cache", got)
		}
	})
	t.Run("default", func(t *testing.T) {
		t.Setenv("HF_HOME", "")
		t.Setenv("HF_HUB_CACHE", "")
		if got := hfCacheDir(); !strings.HasSuffix(got, filepath.Join(".cache", "huggingface", "hub")) {
			t.Fatalf("hfCacheDir() = %q, want the documented default", got)
		}
	})
}

// TestLoadRestartsEngineIsDerivedNotListed proves the remote readiness budget
// cannot drift from the manifests.
//
// A hand-kept engine list is what cut a restarting load off at 30 s. Any engine
// that templates {model} in its launch args restarts to load, and this must
// agree with the bundled manifests without anyone updating a second place.
func TestLoadRestartsEngineIsDerivedNotListed(t *testing.T) {
	reg := NewRegistry()
	if err := reg.LoadFS(bundledManifests, "manifests"); err != nil {
		t.Fatalf("load bundled manifests: %v", err)
	}
	for _, name := range reg.Names() {
		m, ok := reg.Get(name)
		if !ok {
			continue
		}
		p, ok := m.HostPlatform()
		if !ok {
			continue
		}
		want := runtimeNeedsModel(p.Runtime)
		if got := loadRestartsEngine(name); got != want {
			t.Errorf("loadRestartsEngine(%q) = %v, want %v (derived from its manifest)", name, got, want)
		}
	}
	if loadRestartsEngine("no-such-engine") {
		t.Error("an unknown engine claimed a restarting load")
	}
}

// TestCmdActionsResolveModelsDir pins that a command-based model listing can
// name the directory it enumerates.
//
// models_dir was seeded only for remove_path actions, which was invisible while
// the one engine using it deleted by path. An engine whose list_models is a
// command failed at runtime with "unresolved placeholder {models_dir}" — caught
// by a live test, not by the suite, which is why this exists.
func TestCmdActionsResolveModelsDir(t *testing.T) {
	m := testEngineManifest(fakeEngineBin)
	m.Engine = "max"
	m.DisplayName = "max"
	m.Actions = map[string]Action{
		"list_models": {Cmd: []string{fakeEngineBin, "echo", "{models_dir}"}},
	}
	ex := newTestExecutor(t, m)
	st, err := ex.state("max")
	if err != nil {
		t.Fatal(err)
	}
	st.mu.Lock()
	st.installed, st.running, st.healthy = true, true, true
	st.mu.Unlock()

	out, err := ex.Action(context.Background(), "max", "list_models", nil)
	if err != nil {
		t.Fatalf("list_models: %v", err)
	}
	want := engineModelsDir("max")
	if !strings.Contains(string(out), want) {
		t.Fatalf("list_models output = %s, want it to contain the resolved models dir %q", out, want)
	}
}
