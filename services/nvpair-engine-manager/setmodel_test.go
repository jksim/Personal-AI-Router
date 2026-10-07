// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// modelEngineFixture is a stopped, installed engine whose launch args template
// {model} — the shape that makes it model-configured.
func modelEngineFixture(t *testing.T, engine string) *Executor {
	t.Helper()
	m := testEngineManifest(fakeEngineBin)
	m.Engine = engine
	m.DisplayName = engine
	for key, platform := range m.Platforms {
		platform.Runtime = Runtime{
			Bin:  fakeEngineBin,
			Args: []string{"serve", "--model", "{model}", "--port", "{port}"},
			Port: 18000,
			Stop: &StopSpec{Signal: "term", GraceS: 3},
		}
		m.Platforms[key] = platform
	}
	ex := newTestExecutor(t, m)
	ex.overrideDir = t.TempDir()
	st, err := ex.state(engine)
	if err != nil {
		t.Fatal(err)
	}
	st.mu.Lock()
	st.installed = true
	st.mu.Unlock()
	return ex
}

// TestSetModelPersistsAsARuntimeOverride pins that the selection survives a
// restart the same way a chosen port does. A model kept only in memory would be
// silently forgotten, and the engine would refuse to start next launch.
func TestSetModelPersistsAsARuntimeOverride(t *testing.T) {
	ex := modelEngineFixture(t, "max")
	if _, err := ex.SetModel(context.Background(), "max", "modularai/Llama-3.1-8B"); err != nil {
		t.Fatalf("SetModel() error = %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(ex.overrideDir, "max.json"))
	if err != nil {
		t.Fatalf("read override: %v", err)
	}
	var override struct {
		Engine  string `json:"engine"`
		Runtime struct {
			Model string `json:"model"`
		} `json:"runtime"`
	}
	if err := json.Unmarshal(raw, &override); err != nil {
		t.Fatalf("parse override: %v", err)
	}
	if override.Engine != "max" || override.Runtime.Model != "modularai/Llama-3.1-8B" {
		t.Fatalf("override = %+v, want the selected model under runtime.model", override)
	}

	st, err := ex.state("max")
	if err != nil {
		t.Fatal(err)
	}
	if got := st.plat.Runtime.Model; got != "modularai/Llama-3.1-8B" {
		t.Fatalf("in-memory runtime model = %q, want it applied without a reload", got)
	}
}

// TestSetModelRefusesAnAdoptedEngine inherits SetPort's rule. A `max serve`
// NVPAIR adopted rather than started is someone else's process; relaunching it
// onto a different model would kill something NVPAIR does not own.
func TestSetModelRefusesAnAdoptedEngine(t *testing.T) {
	m := testEngineManifest(fakeEngineBin)
	m.Engine = "max"
	m.DisplayName = "max"
	for key, platform := range m.Platforms {
		platform.Runtime = Runtime{
			Bin:  fakeEngineBin,
			Args: []string{"serve", "--model", "{model}"},
			Port: 18000,
			Stop: &StopSpec{Signal: "term", GraceS: 3},
		}
		m.Platforms[key] = platform
	}
	ex := newTestExecutor(t, m)
	ex.overrideDir = t.TempDir()
	st, err := ex.state("max")
	if err != nil {
		t.Fatal(err)
	}
	st.mu.Lock()
	st.installed, st.running, st.adopted, st.port = true, true, true, 18000
	st.mu.Unlock()

	_, err = ex.SetModel(context.Background(), "max", "modularai/Llama-3.1-8B")
	if err == nil || !strings.Contains(err.Error(), "external management") {
		t.Fatalf("SetModel() on an adopted engine = %v, want a refusal naming external management", err)
	}
	if _, statErr := os.Stat(filepath.Join(ex.overrideDir, "max.json")); statErr == nil {
		t.Fatal("a refused SetModel still persisted an override")
	}
}

// TestSetModelRejectsAnEngineThatDoesNotSelectAtLaunch keeps the two model
// concepts apart. Ollama loads a model into a running server; asking it to
// "set" one would restart it for no reason and lose its loaded state.
func TestSetModelRejectsAnEngineThatDoesNotSelectAtLaunch(t *testing.T) {
	m := testEngineManifest(fakeEngineBin)
	m.Engine = "plain"
	m.DisplayName = "plain"
	ex := newTestExecutor(t, m)
	ex.overrideDir = t.TempDir()

	_, err := ex.SetModel(context.Background(), "plain", "some/model")
	if err == nil || !strings.Contains(err.Error(), "does not select a model at launch") {
		t.Fatalf("SetModel() on a non-launch-configured engine = %v, want a refusal", err)
	}
}

func TestValidModelRef(t *testing.T) {
	cases := []struct {
		name  string
		model string
		ok    bool
	}{
		{name: "hugging face repo id", model: "modularai/Llama-3.1-8B", ok: true},
		{name: "local path", model: "/models/llama", ok: true},
		{name: "empty", model: ""},
		{name: "newline", model: "a\nb"},
		{name: "looks like a flag", model: "--help"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validModelRef(tc.model)
			if tc.ok && err != nil {
				t.Fatalf("validModelRef(%q) = %v, want accepted", tc.model, err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("validModelRef(%q) = nil, want rejected", tc.model)
			}
		})
	}
}

// TestLoadModelActionSelectsTheLaunchModel pins the route the desktop actually
// takes. The UI sends engine:action{load_model} for every engine that is not
// Ollama; MAX has no such action and cannot have one, so without this the
// model picker fails with "engine has no action load_model".
func TestLoadModelActionSelectsTheLaunchModel(t *testing.T) {
	ex := modelEngineFixture(t, "max")
	out, err := ex.Action(context.Background(), "max", "load_model",
		json.RawMessage(`{"model":"modularai/Llama-3.1-8B"}`))
	if err != nil {
		t.Fatalf("load_model: %v", err)
	}
	if !strings.Contains(string(out), `"engine":"max"`) {
		t.Fatalf("load_model result = %s, want an engine status", out)
	}
	st, err := ex.state("max")
	if err != nil {
		t.Fatal(err)
	}
	if got := st.plat.Runtime.Model; got != "modularai/Llama-3.1-8B" {
		t.Fatalf("selected model = %q, want the one load_model named", got)
	}
}

// TestLoadModelActionRejectsAnEmptyModel keeps a missing param from silently
// selecting nothing and leaving the engine unstartable.
func TestLoadModelActionRejectsAnEmptyModel(t *testing.T) {
	ex := modelEngineFixture(t, "max")
	if _, err := ex.Action(context.Background(), "max", "load_model", nil); err == nil {
		t.Fatal("load_model with no params succeeded, want a rejection")
	}
}
