// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestExtractLinesResult covers the inventory shape a command-based engine
// reports in.
//
// An engine with no model-list endpoint enumerates its own store and prints
// what it finds. Without this the extractor only understood JSON, so such an
// engine contributed no available models at all: its models sat on disk, the
// node advertised none, and routing refused every request for them with "no
// available node advertises the requested model".
func TestExtractLinesResult(t *testing.T) {
	spec := &ActionResult{Lines: true}
	cases := []struct {
		name string
		raw  string
		want []string
	}{
		{
			name: "one model per line",
			raw:  `"modularai/Llama-3.1-8B\nHuggingFaceTB/SmolLM2-135M-Instruct\n"`,
			want: []string{"modularai/Llama-3.1-8B", "HuggingFaceTB/SmolLM2-135M-Instruct"},
		},
		{
			// An engine installed with nothing downloaded is a successful empty
			// inventory. Reporting failure would drop the engine's key entirely,
			// which a consumer reads as "not queryable" rather than "none yet".
			name: "no models is an empty inventory, not a failure",
			raw:  `""`,
			want: []string{},
		},
		{
			name: "blank and padded lines are ignored",
			raw:  `"\n  a/b  \n\n c/d \n"`,
			want: []string{"a/b", "c/d"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := extractStringsResult(json.RawMessage(tc.raw), spec)
			if !ok {
				t.Fatalf("extractStringsResult(%s) reported failure", tc.raw)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}

	// A JSON object is not line output; saying so beats silently listing nothing.
	if _, ok := extractStringsResult(json.RawMessage(`{"models":[]}`), spec); ok {
		t.Fatal("a JSON object was accepted as line output")
	}
}

// TestActionResultRequiresOneShape pins the validation. A result spec that
// declares neither shape silently extracts nothing, which is the failure mode
// this whole change exists to remove.
func TestActionResultRequiresOneShape(t *testing.T) {
	cases := []struct {
		name    string
		result  *ActionResult
		wantErr string
	}{
		{name: "array and field", result: &ActionResult{Array: "data", Field: "id"}},
		{name: "lines", result: &ActionResult{Lines: true}},
		{name: "neither", result: &ActionResult{}, wantErr: "either result.lines or both"},
		{name: "array without field", result: &ActionResult{Array: "data"}, wantErr: "either result.lines or both"},
		{
			name:    "both shapes at once",
			result:  &ActionResult{Lines: true, Array: "data", Field: "id"},
			wantErr: "cannot be combined",
		},
		{
			name:    "lines with a row filter",
			result:  &ActionResult{Lines: true, Match: &ResultMatch{Field: "state", In: []string{"loaded"}}},
			wantErr: "cannot be used with result.lines",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := validManifest()
			m.Actions = map[string]Action{
				"list_models": {Cmd: []string{fakeEngineBin, "echo", "x"}, Result: tc.result},
			}
			err := m.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want accepted", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() = nil, want an error containing %q", tc.wantErr)
			}
		})
	}
}

// TestBundledListModelsContributeToTheInventory is the check that was missing.
//
// ModelsResult only queries an engine whose list_models declares a result
// extraction spec; without one the engine is skipped entirely. MAX shipped like
// that: the golden test was satisfied because the action existed, the models
// downloaded fine, and the node advertised none of them — so the model manager
// showed an empty list and routing refused every request for a model that was
// sitting on disk.
//
// Declaring the action is not enough. It has to produce an inventory.
func TestBundledListModelsContributeToTheInventory(t *testing.T) {
	reg := NewRegistry()
	if err := reg.LoadFS(bundledManifests, "manifests"); err != nil {
		t.Fatalf("bundled manifests invalid: %v", err)
	}
	for _, name := range reg.Names() {
		m, ok := reg.Get(name)
		if !ok {
			continue
		}
		act, ok := m.Actions["list_models"]
		if !ok {
			continue // the golden test already requires the action itself
		}
		if act.Result == nil {
			t.Errorf("bundled %q declares list_models with no result spec, so it contributes no models "+
				"to the node inventory and nothing can route to them", name)
		}
	}
}
