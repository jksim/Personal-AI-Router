// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

//go:build live

// Opt-in: PAIR routing inference to a MAX engine it manages itself.
//
//	go test -tags live -run TestLiveMaxRoutedThroughPair -v -timeout 2400s  # needs NVPAIR_LIVE_MAX=1
package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"testing"

	"nvpair-shared/jsonrpc"
	"time"
)

// TestLiveMaxRoutedThroughPair is the end-to-end claim of this release: a
// client pointed at PAIR reaches a MAX engine PAIR installed, started and
// routes to — with no MAX-specific code on the client side.
//
// Unlike the cross-process routing tests, the upstream here is a real MAX
// process rather than an httptest stub. That is the point: stubs proved the
// proxy's routing logic, and a stub cannot show that the engine PAIR manages is
// reachable through it.
func TestLiveMaxRoutedThroughPair(t *testing.T) {
	if os.Getenv("NVPAIR_LIVE_MAX") == "" {
		t.Skip("set NVPAIR_LIVE_MAX=1 to route real inference through a managed MAX engine")
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skipf("the MAX manifest declares linux/amd64 only; this is %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	model := os.Getenv("NVPAIR_LIVE_MAX_MODEL")
	if model == "" {
		model = "HuggingFaceTB/SmolLM2-135M-Instruct"
	}

	// node-info is not optional here even though the broker treats it as such:
	// the proxy routes to nodes it learns from discovery, and a node with no
	// advertised inventory is not a candidate however healthy its engine is.
	stdin, msgs, stderr, cleanup := startBrokerWith(t,
		"--max-proxy-path", maxProxyBin,
		"--engine-manager-path", engineMgrBin,
		"--node-info-path", nodeInfoBin,
	)
	t.Cleanup(cleanup)
	go func() {
		for range stderr {
		}
	}()

	waitForMethod(t, msgs, "app:ready", 20*time.Second)
	proxyPort := waitMaxProxyReady(t, stdin, msgs, 30*time.Second)
	t.Logf("max-proxy listening on %d", proxyPort)

	// Install is a no-op when a previous run already did it; the engine-manager
	// detects the venv and reports installed.
	maxRPC(t, stdin, msgs, 700, "engine:install", map[string]any{"engine": "max"}, 1800*time.Second)

	// Select the model, then start. Selection is launch configuration on this
	// engine, so it has to happen before the process comes up.
	maxRPC(t, stdin, msgs, 701, "engine:action", map[string]any{
		"engine": "max", "action": "load_model", "params": map[string]string{"model": model},
	}, 180*time.Second)
	startResult := maxRPC(t, stdin, msgs, 702, "engine:start", map[string]any{"engine": "max"}, 1200*time.Second)
	if !strings.Contains(string(startResult), `"running":true`) {
		t.Fatalf("MAX did not start: %s", startResult)
	}

	// The engine's models must reach the node inventory, or nothing is routable
	// to it however healthy the engine is.
	if !waitForMaxInventory(t, stdin, msgs, model, 120*time.Second) {
		t.Fatalf("model %q never reached the node inventory, so nothing can route to it", model)
	}

	// Reaching engine-manager's inventory is not the same as reaching the
	// proxy's candidate set: the first is local state, the second arrives over
	// discovery. Routing depends on the second, so wait for it explicitly rather
	// than sending a request into a race.
	if !waitForMaxProxyCandidate(t, stdin, msgs, model, 120*time.Second) {
		t.Fatalf("max-proxy never saw a node advertising %q", model)
	}

	client := &http.Client{Timeout: 180 * time.Second}
	t.Cleanup(client.CloseIdleConnections)
	endpoint := fmt.Sprintf("http://127.0.0.1:%d/v1/chat/completions", proxyPort)

	body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"Say OK."}],"max_tokens":16}`, model)
	resp, err := client.Post(endpoint, "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("routed completion failed: %v", err)
	}
	payload, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("routed completion = %d %s", resp.StatusCode, payload)
	}
	if !strings.Contains(string(payload), "choices") {
		t.Fatalf("routed completion had no choices: %s", payload)
	}
	t.Logf("routed a completion through PAIR to the managed MAX engine")

	// The proxy must merge the engine's inventory into the cluster model list.
	modelsResp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/v1/models", proxyPort))
	if err != nil {
		t.Fatalf("proxy /v1/models: %v", err)
	}
	modelsBody, _ := io.ReadAll(modelsResp.Body)
	_ = modelsResp.Body.Close()
	if !strings.Contains(string(modelsBody), model) {
		t.Errorf("proxy /v1/models omits the served model: %s", modelsBody)
	}

	// A model nobody serves must be refused locally, without touching the engine.
	unknown, err := client.Post(endpoint, "application/json",
		bytes.NewBufferString(`{"model":"nobody/serves-this","messages":[]}`))
	if err != nil {
		t.Fatalf("ownerless request failed: %v", err)
	}
	unknownBody, _ := io.ReadAll(unknown.Body)
	_ = unknown.Body.Close()
	if unknown.StatusCode != http.StatusBadGateway ||
		!strings.Contains(string(unknownBody), "no available node advertises the requested model") {
		t.Errorf("ownerless model = %d %s, want a local 502", unknown.StatusCode, unknownBody)
	}

	maxRPC(t, stdin, msgs, 710, "engine:stop", map[string]any{"engine": "max"}, 180*time.Second)
}

// waitForMaxInventory polls until the node reports the model under the max
// engine. Inventory is swept on a timer, so it is not instant after a start.
func waitForMaxInventory(t *testing.T, stdin io.Writer, msgs <-chan jsonrpc.Message, model string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.After(timeout)
	id := 8000
	for {
		id++
		raw := maxRPC(t, stdin, msgs, id, "engine:models", nil, 30*time.Second)
		var got struct {
			ByEngine map[string][]string `json:"modelsByEngine"`
		}
		if json.Unmarshal(raw, &got) == nil {
			for _, m := range got.ByEngine["max"] {
				if m == model {
					return true
				}
			}
		}
		select {
		case <-deadline:
			t.Logf("last inventory: %s", raw)
			return false
		case <-time.After(3 * time.Second):
		}
	}
}

// maxRPC is callBrokerRPC with a caller-set deadline and the result returned.
// Install, start and a model switch all outlive the shared five-second budget:
// MAX compiles a graph on load, which is minutes on a cold model.
func maxRPC(t *testing.T, stdin io.Writer, msgs <-chan jsonrpc.Message, id int, method string, params any, timeout time.Duration) json.RawMessage {
	t.Helper()
	frame, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": id, "method": method, "params": params,
	})
	if err != nil {
		t.Fatalf("marshal %s: %v", method, err)
	}
	writeRawFrame(t, stdin, string(frame))
	resp := waitForResponseID(t, msgs, id, timeout)
	if resp.Error != nil {
		t.Fatalf("%s failed: %d %s", method, resp.Error.Code, resp.Error.Message)
	}
	return resp.Result
}

// waitForMaxProxyCandidate polls the proxy's own node list until one advertises
// the model. This is the precondition routing actually tests against.
func waitForMaxProxyCandidate(t *testing.T, stdin io.Writer, msgs <-chan jsonrpc.Message, model string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.After(timeout)
	id := 8500
	for {
		id++
		raw := maxRPC(t, stdin, msgs, id, "max-proxy:nodes/list", nil, 30*time.Second)
		if strings.Contains(string(raw), model) {
			return true
		}
		select {
		case <-deadline:
			t.Logf("last max-proxy node list: %s", raw)
			return false
		case <-time.After(3 * time.Second):
		}
	}
}
