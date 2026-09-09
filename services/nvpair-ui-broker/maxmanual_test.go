// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"net"
	"reflect"
	"testing"
	"time"
)

// maxManualFrame is a node/discovered payload as nvpair-manual-nodes emits it
// for a node running all three engines. It is a literal rather than a
// marshalled struct on purpose: the two structs live in separate Go modules and
// nothing but their JSON tags connects them, so a copy of the real wire text is
// what actually pins the contract. Renaming a tag on either side fails here.
const maxManualFrame = `{
  "id": "manual:10.0.0.9",
  "address": "10.0.0.9",
  "ollama_up": true,
  "ollama_port": 11434,
  "ollama_models": ["llama3"],
  "lmstudio_up": true,
  "lmstudio_port": 1234,
  "lmstudio_models": ["qwen2.5"],
  "max_up": true,
  "max_port": 8000,
  "max_models": ["modularai/Llama-3.1-8B"],
  "node_info_port": 14318,
  "telemetryValid": true,
  "msSince": 120
}`

func TestManualNodeStatusDecodesTheMaxFields(t *testing.T) {
	var status manualNodeStatus
	if err := json.Unmarshal([]byte(maxManualFrame), &status); err != nil {
		t.Fatalf("decode manual node frame: %v", err)
	}
	if !status.MaxUp || status.MaxPort != managedMaxFacadePort {
		t.Fatalf("max reachability = (%v, %d), want (true, %d)", status.MaxUp, status.MaxPort, managedMaxFacadePort)
	}
	if want := []string{"modularai/Llama-3.1-8B"}; !reflect.DeepEqual(status.MaxModels, want) {
		t.Fatalf("max models = %v, want %v", status.MaxModels, want)
	}

	enriched := manualToEnriched(status)
	wantModels := []string{"llama3", "qwen2.5", "modularai/Llama-3.1-8B"}
	if !reflect.DeepEqual(enriched.Models, wantModels) {
		t.Fatalf("merged models = %v, want %v", enriched.Models, wantModels)
	}
	wantByEngine := map[string][]string{
		"ollama":   {"llama3"},
		"lmstudio": {"qwen2.5"},
		"max":      {"modularai/Llama-3.1-8B"},
	}
	if !reflect.DeepEqual(enriched.ModelsByEngine, wantByEngine) {
		t.Fatalf("models by engine = %v, want %v", enriched.ModelsByEngine, wantByEngine)
	}
}

// TestBridgeManualNodeReachesTheMaxProxy pins both directions of the bridge. A
// manual node never appears in the discovery relay's snapshots, so without this
// explicit add the MAX proxy cannot route to it at all; and without the matching
// remove it would keep routing to an engine that has gone away.
func TestBridgeManualNodeReachesTheMaxProxy(t *testing.T) {
	proxyClient, proxyServer := net.Pipe()
	t.Cleanup(func() {
		_ = proxyClient.Close()
		_ = proxyServer.Close()
	})
	proxy := &proxyProcess{peer: NewPeer(NewCodec(proxyClient))}
	go proxy.peer.Serve(nil, nil)

	type call struct {
		method string
		params json.RawMessage
	}
	calls := make(chan call, 4)
	go func() {
		codec := NewCodec(proxyServer)
		for {
			msg, err := codec.Read()
			if err != nil {
				return
			}
			calls <- call{method: msg.Method, params: msg.Params}
			_ = codec.Respond(msg.ID, map[string]bool{"ok": true})
		}
	}()

	b := &Broker{}
	b.setMaxProxy(proxy)

	var status manualNodeStatus
	if err := json.Unmarshal([]byte(maxManualFrame), &status); err != nil {
		t.Fatalf("decode manual node frame: %v", err)
	}
	b.bridgeManualNode(status, "host-uuid")

	got := nextManualCall(t, calls)
	if got.method != "node/add-manual" {
		t.Fatalf("bridge method = %q, want node/add-manual", got.method)
	}
	var node proxyManualNode
	if err := json.Unmarshal(got.params, &node); err != nil {
		t.Fatalf("decode add-manual params: %v", err)
	}
	if node.ID != "host-uuid" || node.Port != managedMaxFacadePort || node.Host != "10.0.0.9" {
		t.Fatalf("bridged node = %+v, want the MAX engine keyed by host uuid", node)
	}

	status.MaxUp = false
	b.bridgeManualNode(status, "host-uuid")
	if got := nextManualCall(t, calls); got.method != "node/remove-manual" {
		t.Fatalf("unreachable MAX produced %q, want node/remove-manual", got.method)
	}

	b.removeManualNodeFromProxies("host-uuid")
	if got := nextManualCall(t, calls); got.method != "node/remove-manual" {
		t.Fatalf("node removal produced %q, want node/remove-manual", got.method)
	}
}

func nextManualCall[T any](t *testing.T, calls chan T) T {
	t.Helper()
	select {
	case c := <-calls:
		return c
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a manual-node bridge call")
		var zero T
		return zero
	}
}
