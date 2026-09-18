// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// TestStoppedEngineStillReportsItsDiskInventory is the bug a user hit: clicking
// load restarted the engine, and while it was down every one of its models
// disappeared from the model manager — the very screen you use to choose what
// it should serve when it comes back.
//
// An engine whose list_models is a command reads its own on-disk store, which
// is there whether or not the server is up.
func TestStoppedEngineStillReportsItsDiskInventory(t *testing.T) {
	m := testEngineManifest(fakeEngineBin)
	m.Engine = "max"
	m.DisplayName = "max"
	m.Actions = map[string]Action{
		"list_models": {
			Cmd:    []string{fakeEngineBin, "echo", "owner/one"},
			Result: &ActionResult{Lines: true},
		},
	}
	ex := newTestExecutor(t, m)
	st, err := ex.state("max")
	if err != nil {
		t.Fatal(err)
	}
	st.mu.Lock()
	st.installed = true
	st.running = false // stopped, exactly as during a load-triggered restart
	st.mu.Unlock()

	got := ex.ModelsResult(context.Background())
	if len(got.ByEngine["max"]) != 1 || got.ByEngine["max"][0] != "owner/one" {
		t.Fatalf("stopped engine reported %v, want its on-disk inventory", got.ByEngine["max"])
	}
	if len(got.Models) != 1 || got.Models[0] != "owner/one" {
		t.Fatalf("flat union = %v, want the stopped engine's models", got.Models)
	}
}

// TestStoppedEngineReportsNothingLoaded keeps the other half honest: what is
// resident in memory is not knowable while the process is down, and claiming a
// model is loaded would route inference to an engine that is not there.
func TestStoppedEngineReportsNothingLoaded(t *testing.T) {
	m := testEngineManifest(fakeEngineBin)
	m.Engine = "max"
	m.DisplayName = "max"
	m.Actions = map[string]Action{
		"list_models": {
			Cmd:    []string{fakeEngineBin, "echo", "owner/one"},
			Result: &ActionResult{Lines: true},
		},
		"loaded_models": {
			HTTP:   &ActionHTTP{Method: "GET", Path: "/v1/models"},
			Result: &ActionResult{Array: "data", Field: "id"},
		},
	}
	ex := newTestExecutor(t, m)
	st, err := ex.state("max")
	if err != nil {
		t.Fatal(err)
	}
	st.mu.Lock()
	st.installed = true
	st.running = false
	st.mu.Unlock()

	got := ex.ModelsResult(context.Background())
	if _, claimed := got.LoadedByEngine["max"]; claimed {
		t.Fatalf("a stopped engine claimed loaded models: %v", got.LoadedByEngine)
	}
	if len(got.ByEngine["max"]) != 1 {
		t.Fatalf("stopped engine lost its disk inventory: %v", got.ByEngine)
	}
}

// TestHTTPInventoryStillRequiresARunningEngine pins that this did not loosen
// the rule for engines whose listing is an API call. Querying a dead loopback
// port cannot succeed, and reporting an empty list as authoritative would erase
// a running engine's models on a transient failure.
func TestHTTPInventoryStillRequiresARunningEngine(t *testing.T) {
	m := testEngineManifest(fakeEngineBin)
	m.Engine = "httpish"
	m.DisplayName = "httpish"
	m.Actions = map[string]Action{
		"list_models": {
			HTTP:   &ActionHTTP{Method: "GET", Path: "/v1/models"},
			Result: &ActionResult{Array: "data", Field: "id"},
		},
	}
	ex := newTestExecutor(t, m)
	st, err := ex.state("httpish")
	if err != nil {
		t.Fatal(err)
	}
	st.mu.Lock()
	st.installed = true
	st.running = false
	st.mu.Unlock()

	got := ex.ModelsResult(context.Background())
	if _, present := got.ByEngine["httpish"]; present {
		t.Fatalf("a stopped HTTP-listing engine reported an inventory: %v", got.ByEngine)
	}
}

func TestActionRunsWhileStopped(t *testing.T) {
	cases := []struct {
		name string
		act  Action
		want bool
	}{
		{name: "command", act: Action{Cmd: []string{"ls"}}, want: true},
		{name: "path removal", act: Action{RemovePath: &ActionRemovePath{Root: "/a", Path: "/a/b"}}, want: true},
		{name: "http", act: Action{HTTP: &ActionHTTP{Method: "GET", Path: "/x"}}},
		{name: "absent", act: Action{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := actionRunsWhileStopped(tc.act); got != tc.want {
				t.Fatalf("actionRunsWhileStopped() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestInventoryDoesNotWaitOnAStartingEngine is the regression that reached a
// published branch.
//
// Status takes the engine's operation lock, and a start holds it until the
// engine is ready — minutes, for an engine that compiles a model on first load.
// The inventory sweep called Status, so one starting engine stalled the whole
// model list, which is served over HTTP to cluster peers enriching this node.
// The node looked like it had no models at all, and the sweep's own deadline
// could not help because a mutex does not observe a context.
func TestInventoryDoesNotWaitOnAStartingEngine(t *testing.T) {
	m := testEngineManifest(fakeEngineBin)
	m.Engine = "slowstart"
	m.DisplayName = "slowstart"
	m.Actions = map[string]Action{
		"list_models": {
			Cmd:    []string{fakeEngineBin, "echo", "owner/one"},
			Result: &ActionResult{Lines: true},
		},
	}
	ex := newTestExecutor(t, m)
	st, err := ex.state("slowstart")
	if err != nil {
		t.Fatal(err)
	}
	st.mu.Lock()
	st.installed = true
	st.mu.Unlock()

	// Hold the operation lock the way a start in progress does.
	st.opMu.Lock()
	defer st.opMu.Unlock()

	done := make(chan ModelsResult, 1)
	go func() { done <- ex.ModelsResult(context.Background()) }()

	select {
	case got := <-done:
		if len(got.ByEngine["slowstart"]) != 1 {
			t.Fatalf("inventory = %v, want the engine's cached on-disk models", got.ByEngine)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ModelsResult blocked on an engine whose operation lock is held")
	}
}

// TestInventorySkipsAnUninstalledEngine keeps a listing command from reporting
// another tool's downloads. MAX reads the shared HuggingFace cache, so an
// uninstalled MAX would otherwise advertise models it cannot serve.
func TestInventorySkipsAnUninstalledEngine(t *testing.T) {
	m := testEngineManifest(fakeEngineBin)
	m.Engine = "notinstalled"
	m.DisplayName = "notinstalled"
	m.Actions = map[string]Action{
		"list_models": {
			Cmd:    []string{fakeEngineBin, "echo", "owner/one"},
			Result: &ActionResult{Lines: true},
		},
	}
	// Detection must genuinely fail: the sweep re-detects when the engine is
	// idle, so merely clearing the cached flag would be refreshed straight back.
	for key, plat := range m.Platforms {
		plat.Detect = []string{filepath.Join(t.TempDir(), "absent", "engine")}
		m.Platforms[key] = plat
	}
	ex := newTestExecutor(t, m)

	got := ex.ModelsResult(context.Background())
	if _, present := got.ByEngine["notinstalled"]; present {
		t.Fatalf("an uninstalled engine reported an inventory: %v", got.ByEngine)
	}
}
