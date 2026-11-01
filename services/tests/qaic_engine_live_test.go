// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

//go:build live

// Opt-in: PAIR routing inference to a Qualcomm engine it manages itself.
//
//	go test -tags live -run TestLiveQaicRoutedThroughPair -v -timeout 2400s  # needs NVPAIR_LIVE_QAIC_ENGINE=1
package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"nvpair-shared/jsonrpc"
	"time"
)

// TestLiveQaicRoutedThroughPair is the end-to-end claim of this release: a
// client pointed at PAIR reaches a Qualcomm engine PAIR installed, started and
// routes to — with no Qualcomm-specific code on the client side.
//
// Unlike the cross-process routing tests, the upstream here is a real Qualcomm engine
// process rather than an httptest stub. That is the point: stubs proved the
// proxy's routing logic, and a stub cannot show that the engine PAIR manages is
// reachable through it.
func TestLiveQaicRoutedThroughPair(t *testing.T) {
	if os.Getenv("NVPAIR_LIVE_QAIC_ENGINE") == "" {
		t.Skip("set NVPAIR_LIVE_QAIC_ENGINE=1 to route real inference through the managed Qualcomm engine")
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skipf("the qaic manifest declares linux/amd64 only; this is %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	model := os.Getenv("NVPAIR_LIVE_QAIC_MODEL")
	if model == "" {
		model = "Qwen/Qwen3-30B-A3B"
	}

	// node-info is not optional here even though the broker treats it as such:
	// the proxy routes to nodes it learns from discovery, and a node with no
	// advertised inventory is not a candidate however healthy its engine is.
	// The broker runs with HOME set to its config directory, so the engine's
	// models directory is there too. Link the unpacked QPC in rather than
	// downloading 46 GB again.
	configDir := t.TempDir()
	linkLiveQaicModel(t, configDir, model)
	stdin, msgs, stderr, cleanup := startBrokerWithConfigDirAndEnv(t, configDir, nil,
		// The Qualcomm engine is a facade of the one nvpair-proxy, asked for by name rather
		// than a binary of its own.
		"--proxy-path", proxyBin, "--proxy-engines", "qaic",
		"--engine-manager-path", engineMgrBin,
		"--node-info-path", nodeInfoBin,
	)
	t.Cleanup(cleanup)
	go func() {
		for range stderr {
		}
	}()

	waitForMethod(t, msgs, "app:ready", 20*time.Second)
	proxyPort := waitQaicProxyReady(t, stdin, msgs, 30*time.Second)
	t.Logf("qaic-proxy listening on %d", proxyPort)

	// Install is a no-op when a previous run already did it; the engine-manager
	// detects the venv and reports installed.
	maxRPC(t, stdin, msgs, 700, "engine:install", map[string]any{"engine": "qaic"}, 1800*time.Second)

	// Select the model, then start. Selection is launch configuration on this
	// engine, so it has to happen before the process comes up.
	maxRPC(t, stdin, msgs, 701, "engine:action", map[string]any{
		"engine": "qaic", "action": "load_model", "params": map[string]string{"model": model},
	}, 180*time.Second)
	startResult := maxRPC(t, stdin, msgs, 702, "engine:start", map[string]any{"engine": "qaic"}, 1200*time.Second)
	if !strings.Contains(string(startResult), `"running":true`) {
		t.Fatalf("the Qualcomm engine did not start: %s", startResult)
	}

	// The engine's models must reach the node inventory, or nothing is routable
	// to it however healthy the engine is.
	if !waitForQaicInventory(t, stdin, msgs, model, 120*time.Second) {
		t.Fatalf("model %q never reached the node inventory, so nothing can route to it", model)
	}

	// Reaching engine-manager's inventory is not the same as reaching the
	// proxy's candidate set: the first is local state, the second arrives over
	// discovery. Routing depends on the second, so wait for it explicitly rather
	// than sending a request into a race.
	if !waitForQaicProxyCandidate(t, stdin, msgs, model, 120*time.Second) {
		t.Fatalf("qaic-proxy never saw a node advertising %q", model)
	}

	client := &http.Client{Timeout: 180 * time.Second}
	t.Cleanup(client.CloseIdleConnections)
	endpoint := fmt.Sprintf("http://127.0.0.1:%d/v1/chat/completions", proxyPort)

	body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"Say OK. /no_think"}],"max_tokens":16}`, model)
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
	t.Logf("routed a completion through PAIR to the managed Qualcomm engine")

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

	maxRPC(t, stdin, msgs, 710, "engine:stop", map[string]any{"engine": "qaic"}, 180*time.Second)
}

// waitForQaicInventory polls until the node reports the model under the qaic
// engine. Inventory is swept on a timer, so it is not instant after a start.
func waitForQaicInventory(t *testing.T, stdin io.Writer, msgs <-chan jsonrpc.Message, model string, timeout time.Duration) bool {
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
			for _, m := range got.ByEngine["qaic"] {
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

// waitForQaicProxyCandidate polls the proxy's own node list until one advertises
// the model. This is the precondition routing actually tests against.
func waitForQaicProxyCandidate(t *testing.T, stdin io.Writer, msgs <-chan jsonrpc.Message, model string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.After(timeout)
	id := 8500
	for {
		id++
		raw := maxRPC(t, stdin, msgs, id, "qaic-proxy:nodes/list", nil, 30*time.Second)
		if strings.Contains(string(raw), model) {
			return true
		}
		select {
		case <-deadline:
			t.Logf("last qaic-proxy node list: %s", raw)
			return false
		case <-time.After(3 * time.Second):
		}
	}
}

// waitQaicProxyReady polls qaic-proxy:get-status through the broker until the
// proxy reports a bound port, mirroring waitLMStudioProxyReady.
func waitQaicProxyReady(t *testing.T, stdin io.Writer, msgs <-chan jsonrpc.Message, timeout time.Duration) int {
	t.Helper()
	id := 9600
	writeRawFrame(t, stdin, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"qaic-proxy:get-status"}`, id))
	deadline := time.After(timeout)
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case msg, ok := <-msgs:
			if !ok {
				t.Fatal("broker stream closed waiting for qaic-proxy:get-status")
			}
			if msg.ID != nil && msg.Method == "" {
				var st struct {
					Ready bool `json:"ready"`
					Port  int  `json:"port"`
				}
				if json.Unmarshal(msg.Result, &st) == nil && st.Ready {
					return st.Port
				}
			}
		case <-tick.C:
			id++
			writeRawFrame(t, stdin, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"qaic-proxy:get-status"}`, id))
		case <-deadline:
			t.Fatalf("timed out (%s) waiting for qaic-proxy to report ready", timeout)
			return 0
		}
	}
}

// linkLiveQaicModel links the developer's unpacked QPC into a test HOME.
func linkLiveQaicModel(t *testing.T, home, model string) {
	t.Helper()
	// The suite points HOME at a scratch directory, so read the real one
	// from the user database.
	account, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(account.HomeDir, ".local/share/nvpair/qaic-models", model)
	if _, err := os.Stat(src); err != nil {
		t.Skipf("the QPC for %s is not unpacked at %s", model, src)
	}
	dst := filepath.Join(home, ".local/share/nvpair/qaic-models", model)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(src, dst); err != nil {
		t.Fatal(err)
	}
}
