// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

//go:build live

// Opt-in live validation of the MAX engine against the REAL bundled manifest.
//
//	go test -tags live -run TestLiveMaxCleanRoom -v -timeout 1800s   # needs NVPAIR_LIVE_MAX=1
package main

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestLiveMaxCleanRoom exercises install → start → serve → stop → uninstall
// through the service's own API against the manifest that actually ships.
//
// Unlike the Ollama clean-room test this does not substitute a synthetic
// manifest. The whole point is the bundled one: the pinned uv bootstrap, the
// loopback bind, the /health readiness gate and the HuggingFace model listing
// are what need proving, and a stand-in would prove none of them.
//
// It installs into a temp dir on an auto-assigned port, so it never disturbs a
// MAX a developer already has.
func TestLiveMaxCleanRoom(t *testing.T) {
	if os.Getenv("NVPAIR_LIVE_MAX") == "" {
		t.Skip("set NVPAIR_LIVE_MAX=1 to run the MAX clean-room install test (this downloads ~1.2 GB)")
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skipf("the MAX manifest declares linux/amd64 only; this is %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	reg := NewRegistry()
	if err := reg.LoadFS(bundledManifests, "manifests"); err != nil {
		t.Fatalf("load bundled manifests: %v", err)
	}
	m, ok := reg.Get("max")
	if !ok {
		t.Fatal("bundled max manifest missing")
	}
	// Auto-assign the port so this never collides with a real MAX or with the
	// proxy that squats 8000.
	plat := m.Platforms["linux/amd64"]
	plat.Runtime.Port = 0
	plat.Runtime.Model = liveMaxModel()
	m.Platforms["linux/amd64"] = plat

	frames, stdin, stop := startManagerWithManifest(t, *m)
	defer stop()

	send(t, stdin, 1, "engine:get-installed", nil)
	if r := waitResult(t, frames, "1", 5*time.Second); !strings.Contains(string(r), `"engine":"max"`) {
		t.Fatalf("max not listed: %s", r)
	}

	// The uv bootstrap: fetch a checksum-pinned tarball, create a venv, install
	// max[serve] into it. Measured at ~30s on a warm network, but a cold PyPI
	// mirror is much slower.
	installStart := time.Now()
	send(t, stdin, 2, "engine:install", map[string]string{"engine": "max"})
	if r := waitResult(t, frames, "2", 1200*time.Second); !strings.Contains(string(r), `"installed":true`) {
		t.Fatalf("expected installed:true after install, got %s", r)
	}
	t.Logf("uv bootstrap + max[serve] install took %s", time.Since(installStart).Round(time.Second))

	// First start compiles the model graph, which is minutes, not seconds —
	// the reason the manifest declares a 600s readiness budget.
	startedAt := time.Now()
	send(t, stdin, 3, "engine:start", map[string]string{"engine": "max"})
	if r := waitResult(t, frames, "3", 900*time.Second); !strings.Contains(string(r), `"running":true`) {
		t.Fatalf("expected running:true after start, got %s", r)
	}
	t.Logf("max serve reached /health in %s", time.Since(startedAt).Round(time.Second))

	// loaded_models proves the engine is actually serving the model that was
	// selected at launch, not merely that a port opened.
	send(t, stdin, 4, "engine:action", map[string]any{"engine": "max", "action": "loaded_models"})
	loaded := waitResult(t, frames, "4", 60*time.Second)
	if !strings.Contains(string(loaded), liveMaxModel()) {
		t.Errorf("loaded_models = %s, want the launch-selected model %q", loaded, liveMaxModel())
	}

	// list_models reads the HuggingFace cache, which the install above populated.
	send(t, stdin, 5, "engine:action", map[string]any{"engine": "max", "action": "list_models"})
	if r := waitResult(t, frames, "5", 60*time.Second); !strings.Contains(string(r), liveMaxModel()) {
		t.Errorf("list_models = %s, want the downloaded model listed", r)
	}

	send(t, stdin, 6, "engine:stop", map[string]string{"engine": "max"})
	waitResult(t, frames, "6", 120*time.Second)

	// Success criterion 6: stopping leaves no orphaned max process. The engine
	// spawns worker processes, so a leaked one would keep the GPU allocated and
	// the next start would fail on a busy device rather than anything obvious.
	if leaked := strings.TrimSpace(pgrepMaxServe(t)); leaked != "" {
		t.Errorf("max processes survived engine:stop: %s", leaked)
	}

	send(t, stdin, 7, "engine:uninstall", map[string]string{"engine": "max"})
	waitResult(t, frames, "7", 120*time.Second)
	send(t, stdin, 8, "shutdown", nil)
	waitResult(t, frames, "8", 5*time.Second)
}

// liveMaxModel is the model the live test serves. Small on purpose: the test
// proves the lifecycle, not the model.
func liveMaxModel() string {
	if m := os.Getenv("NVPAIR_LIVE_MAX_MODEL"); m != "" {
		return m
	}
	return "HuggingFaceTB/SmolLM2-135M-Instruct"
}

// pgrepMaxServe lists surviving `max serve` processes. An empty result (pgrep
// exit 1) is the expected outcome.
func pgrepMaxServe(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("pgrep", "-af", "max serve").Output()
	if err != nil {
		return "" // exit 1 == no matches
	}
	return string(out)
}
