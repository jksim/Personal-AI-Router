// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestManualNodeStatusEmitsTheQaicWireNames pins the three tag names the broker
// decodes. The two structs live in separate Go modules and nothing but these
// strings connects them, so renaming a field here silently stops the Qualcomm engine bridge
// rather than failing to compile.
func TestManualNodeStatusEmitsTheQaicWireNames(t *testing.T) {
	raw, err := json.Marshal(ManualNodeStatus{
		ID:         "manual:10.0.0.9",
		Address:    "10.0.0.9",
		QaicUp:     true,
		QaicPort:   qaicPort,
		QaicModels: []string{"Qwen/Qwen3-30B-A3B"},
	})
	if err != nil {
		t.Fatalf("marshal status: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if decoded["qaic_up"] != true {
		t.Errorf("qaic_up = %v, want true", decoded["qaic_up"])
	}
	if got, _ := decoded["qaic_port"].(float64); int(got) != qaicPort {
		t.Errorf("qaic_port = %v, want %d", decoded["qaic_port"], qaicPort)
	}
	models, _ := decoded["qaic_models"].([]any)
	if len(models) != 1 || models[0] != "Qwen/Qwen3-30B-A3B" {
		t.Errorf("qaic_models = %v, want one model", decoded["qaic_models"])
	}
}

// TestProbeQaicReadsTheOpenAIModelList covers the probe's three outcomes: a
// served model list, a reachable server whose body does not parse (still up, so
// a node is not dropped over a response shape), and an unreachable one.
func TestProbeQaicReadsTheOpenAIModelList(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		body       string
		wantUp     bool
		wantModels []string
	}{
		{
			name:       "model list",
			status:     http.StatusOK,
			body:       `{"data":[{"id":"Qwen/Qwen3-30B-A3B"},{"id":""}]}`,
			wantUp:     true,
			wantModels: []string{"Qwen/Qwen3-30B-A3B"},
		},
		{name: "reachable but unparseable", status: http.StatusOK, body: `not json`, wantUp: true},
		{name: "not serving", status: http.StatusNotFound, body: ``},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotPath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			m := &Manager{client: &http.Client{Timeout: 2 * time.Second}}
			host, port := splitHostPort(t, srv.URL)
			up, models := m.probeQaic(host, port)
			if up != tc.wantUp {
				t.Fatalf("up = %v, want %v", up, tc.wantUp)
			}
			if len(models) != len(tc.wantModels) {
				t.Fatalf("models = %v, want %v", models, tc.wantModels)
			}
			for i := range models {
				if models[i] != tc.wantModels[i] {
					t.Fatalf("models = %v, want %v", models, tc.wantModels)
				}
			}
			if gotPath != "/v1/models" {
				t.Fatalf("probed %q, want /v1/models", gotPath)
			}
		})
	}
}

func TestProbeQaicUnreachable(t *testing.T) {
	m := &Manager{client: &http.Client{Timeout: 200 * time.Millisecond}}
	// Port 1 is reserved and never served; an immediate refusal is the point.
	if up, models := m.probeQaic("127.0.0.1", 1); up || models != nil {
		t.Fatalf("probeQaic on a closed port = (%v, %v), want (false, nil)", up, models)
	}
}
