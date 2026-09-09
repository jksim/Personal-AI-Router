// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

package tests

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"nvpair-shared/appdir"
	"nvpair-shared/jsonrpc"
)

// waitMaxProxyReady polls max-proxy:get-status through the broker until the
// proxy reports a bound port, mirroring waitLMStudioProxyReady.
func waitMaxProxyReady(t *testing.T, stdin io.Writer, msgs <-chan jsonrpc.Message, timeout time.Duration) int {
	t.Helper()
	id := 9600
	writeRawFrame(t, stdin, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"max-proxy:get-status"}`, id))
	deadline := time.After(timeout)
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case msg, ok := <-msgs:
			if !ok {
				t.Fatal("broker stream closed waiting for max-proxy:get-status")
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
			writeRawFrame(t, stdin, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"max-proxy:get-status"}`, id))
		case <-deadline:
			t.Fatalf("timed out (%s) waiting for max-proxy to report ready", timeout)
			return 0
		}
	}
}

// fakeMax stands up a minimal OpenAI-compatible server on MAX's default port,
// which is what the manual prober looks for. Skips when the port is taken.
func fakeMax(t *testing.T) func() {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:8000")
	if err != nil {
		t.Skipf("cannot bind fake MAX on 127.0.0.1:8000 (%v); skipping", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"modularai/Llama-3.1-8B","object":"model"}]}`)
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	return func() { _ = srv.Close() }
}

// persistMaxProxyPort points the app data dir at a temp root and pre-seeds
// max-proxy's persisted port, so the proxy comes up somewhere other than the
// port the fake engine occupies.
func persistMaxProxyPort(t *testing.T, port int) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("LOCALAPPDATA", root)
	t.Setenv("APPDATA", root)
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("HOME", root)
	path, err := appdir.Path("max-proxy-port.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(fmt.Sprintf(`{"port":%d}`, port)), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestBrokerBridgesManualNodeIntoMaxProxy is the MAX counterpart of the Ollama
// and LM Studio bridge tests. A manual node never appears over mDNS, so unless
// the broker explicitly hands it to max-proxy the proxy cannot route to it —
// and the failure is silent, because the proxy simply reports no candidates.
func TestBrokerBridgesManualNodeIntoMaxProxy(t *testing.T) {
	configDir := persistMaxProxyPort(t, freePort(t))
	stopMax := fakeMax(t) // skips if 8000 is unavailable
	t.Cleanup(stopMax)

	stdin, msgs, _, cleanup := startBrokerWithConfigDir(t, configDir,
		"--manual-nodes-path", manualNodesBin,
		"--max-proxy-path", maxProxyBin,
	)
	t.Cleanup(cleanup)

	waitForMethod(t, msgs, "app:ready", 10*time.Second)

	const nodeName = "xproc-manual-max-bridge"
	addReq := fmt.Sprintf(`{"jsonrpc":"2.0","id":940,"method":"node/add","params":{"address":"127.0.0.1","name":%q}}`, nodeName) + "\n"
	if _, err := stdin.Write([]byte(addReq)); err != nil {
		t.Fatalf("write node/add: %v", err)
	}

	deadline := time.After(25 * time.Second)
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	reqID := 941
	sendReq(t, stdin, reqID, "max-proxy:nodes/list")
	for {
		select {
		case msg, ok := <-msgs:
			if !ok {
				t.Fatal("broker stream closed before the manual node bridged into max-proxy")
			}
			if msg.Method == "" && msg.ID != nil && proxyNodesHas(t, msg.Result, nodeName) {
				t.Logf("manual node %q bridged into max-proxy nodes/list", nodeName)
				return
			}
		case <-ticker.C:
			reqID++
			sendReq(t, stdin, reqID, "max-proxy:nodes/list")
		case <-deadline:
			t.Fatalf("timed out waiting for manual node %q in max-proxy:nodes/list", nodeName)
		}
	}
}
