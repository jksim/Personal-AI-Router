// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

//go:build live

// Opt-in: several models, pulled and loaded through the real bundled manifest.
//
//	go test -tags live -run TestLiveMaxModelLifecycle -v -timeout 3600s  # needs NVPAIR_LIVE_MAX=1
package main

import (
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// liveMaxModels are small text-generation checkpoints. Small on purpose: this
// proves the pull → select → serve loop works for more than one model, not that
// any particular model is good.
func liveMaxModels() []string {
	if list := os.Getenv("NVPAIR_LIVE_MAX_MODELS"); list != "" {
		return strings.Split(list, ",")
	}
	return []string{
		"HuggingFaceTB/SmolLM2-135M-Instruct",
		"Qwen/Qwen2.5-0.5B-Instruct",
	}
}

// TestLiveMaxModelLifecycle pulls each model, selects it, and confirms the
// engine actually serves that model — the loop a user drives from the model
// manager.
//
// Selecting a model on MAX is a relaunch, so this also covers the case that
// broke in front of a user: switching from one model to another, where the
// engine stops, comes back on the new model, and must not be left down.
func TestLiveMaxModelLifecycle(t *testing.T) {
	if os.Getenv("NVPAIR_LIVE_MAX") == "" {
		t.Skip("set NVPAIR_LIVE_MAX=1 to run the MAX model lifecycle test (this downloads models)")
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
	models := liveMaxModels()
	plat := m.Platforms["linux/amd64"]
	plat.Runtime.Port = 0 // never collide with a real MAX or the proxy on 8000
	plat.Runtime.Model = models[0]
	m.Platforms["linux/amd64"] = plat

	frames, stdin, stop := startManagerWithManifest(t, *m)
	defer stop()

	send(t, stdin, 1, "engine:install", map[string]string{"engine": "max"})
	if r := waitResult(t, frames, "1", 1800*time.Second); !strings.Contains(string(r), `"installed":true`) {
		t.Fatalf("install: %s", r)
	}

	id := 10
	for _, model := range models {
		t.Run(model, func(t *testing.T) {
			// Pull. MAX would fetch on first launch anyway, but the model
			// manager's download has to work on its own.
			id++
			pullID := id
			send(t, stdin, pullID, "engine:action", map[string]any{
				"engine": "max", "action": "pull_model", "params": map[string]string{"model": model},
			})
			waitResult(t, frames, strconv.Itoa(pullID), 1800*time.Second)

			// The pulled model must appear in the inventory. This is the step
			// that silently did nothing: the files landed and the node
			// advertised none of them.
			id++
			listID := id
			send(t, stdin, listID, "engine:action", map[string]any{"engine": "max", "action": "list_models"})
			if r := waitResult(t, frames, strconv.Itoa(listID), 120*time.Second); !strings.Contains(string(r), model) {
				t.Fatalf("list_models does not include %q after pull: %s", model, r)
			}

			// Select it. On MAX this is a relaunch, not an in-place load.
			id++
			loadID := id
			send(t, stdin, loadID, "engine:action", map[string]any{
				"engine": "max", "action": "load_model", "params": map[string]string{"model": model},
			})
			waitResult(t, frames, strconv.Itoa(loadID), 120*time.Second)

			id++
			startID := id
			send(t, stdin, startID, "engine:start", map[string]string{"engine": "max"})
			if r := waitResult(t, frames, strconv.Itoa(startID), 1200*time.Second); !strings.Contains(string(r), `"running":true`) {
				t.Fatalf("engine did not come up on %q: %s", model, r)
			}

			// Serving the model that was selected — not merely running.
			id++
			loadedID := id
			send(t, stdin, loadedID, "engine:action", map[string]any{"engine": "max", "action": "loaded_models"})
			if r := waitResult(t, frames, strconv.Itoa(loadedID), 120*time.Second); !strings.Contains(string(r), model) {
				t.Fatalf("engine is up but not serving %q: %s", model, r)
			}

			// Real inference, so "serving" means it answers rather than that a
			// port opened.
			id++
			chatID := id
			send(t, stdin, chatID, "engine:action", map[string]any{
				"engine": "max", "action": "chat",
				"params": map[string]any{
					"model":    model,
					"messages": []map[string]string{{"role": "user", "content": "Say OK."}},
				},
			})
			if r := waitResult(t, frames, strconv.Itoa(chatID), 300*time.Second); !strings.Contains(string(r), "choices") {
				t.Fatalf("no completion from %q: %s", model, r)
			}
			t.Logf("%s: pulled, selected, served, answered", model)
		})
	}

	// A stopped engine must still report what is on disk — otherwise the model
	// manager empties out exactly when a user goes there to pick the next one.
	id++
	stopID := id
	send(t, stdin, stopID, "engine:stop", map[string]string{"engine": "max"})
	waitResult(t, frames, strconv.Itoa(stopID), 180*time.Second)

	id++
	stoppedListID := id
	send(t, stdin, stoppedListID, "engine:action", map[string]any{"engine": "max", "action": "list_models"})
	stoppedList := waitResult(t, frames, strconv.Itoa(stoppedListID), 120*time.Second)
	for _, model := range models {
		if !strings.Contains(string(stoppedList), model) {
			t.Errorf("stopped engine dropped %q from its inventory: %s", model, stoppedList)
		}
	}

	id++
	send(t, stdin, id, "shutdown", nil)
	waitResult(t, frames, strconv.Itoa(id), 10*time.Second)
}
