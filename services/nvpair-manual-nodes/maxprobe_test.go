// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestManualNodeStatusEmitsTheMaxWireNames pins the three tag names the broker
// decodes. The two structs live in separate Go modules and nothing but these
// strings connects them, so renaming a field here silently stops the MAX bridge
// rather than failing to compile.
func TestManualNodeStatusEmitsTheMaxWireNames(t *testing.T) {
	raw, err := json.Marshal(ManualNodeStatus{
		ID:        "manual:10.0.0.9",
		Address:   "10.0.0.9",
		MaxUp:     true,
		MaxPort:   maxPort,
		MaxModels: []string{"modularai/Llama-3.1-8B"},
	})
	if err != nil {
		t.Fatalf("marshal status: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if decoded["max_up"] != true {
		t.Errorf("max_up = %v, want true", decoded["max_up"])
	}
	if got, _ := decoded["max_port"].(float64); int(got) != maxPort {
		t.Errorf("max_port = %v, want %d", decoded["max_port"], maxPort)
	}
	models, _ := decoded["max_models"].([]any)
	if len(models) != 1 || models[0] != "modularai/Llama-3.1-8B" {
		t.Errorf("max_models = %v, want one model", decoded["max_models"])
	}
}

// TestProbeMaxReadsTheOpenAIModelList covers the probe's three outcomes: a
// served model list, a reachable server whose body does not parse (still up, so
// a node is not dropped over a response shape), and an unreachable one.
func TestProbeMaxReadsTheOpenAIModelList(t *testing.T) {
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
			body:       `{"data":[{"id":"modularai/Llama-3.1-8B"},{"id":""}]}`,
			wantUp:     true,
			wantModels: []string{"modularai/Llama-3.1-8B"},
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
			up, models := m.probeMax(host, port)
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

func TestProbeMaxUnreachable(t *testing.T) {
	m := &Manager{client: &http.Client{Timeout: 200 * time.Millisecond}}
	// Port 1 is reserved and never served; an immediate refusal is the point.
	if up, models := m.probeMax("127.0.0.1", 1); up || models != nil {
		t.Fatalf("probeMax on a closed port = (%v, %v), want (false, nil)", up, models)
	}
}

func splitHostPort(t *testing.T, rawURL string) (string, int) {
	t.Helper()
	trimmed := strings.TrimPrefix(rawURL, "http://")
	host, portText, found := strings.Cut(trimmed, ":")
	if !found {
		t.Fatalf("test server URL %q has no port", rawURL)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("test server port %q: %v", portText, err)
	}
	return host, port
}
