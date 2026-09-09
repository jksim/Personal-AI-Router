// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"net"
	"testing"
	"time"

	"nvpair-ui-broker/relay"
)

// TestLocalMaxEnginePortDistinguishesAbsentFromUnreachable is the reason this
// engine does not reuse localEnginePort.
//
// MAX need not be in engine-manager's registry, so an RPC error is the ordinary
// answer on a host with no MAX engine — not a sign the manager is broken.
// Applying the stock-port fallback there would let any OpenAI-compatible server
// answering on 8000 be advertised to the cluster as this node's MAX engine. An
// absent manager is different: there is no information either way, and the
// fallback is what the other engines do.
func TestLocalMaxEnginePortDistinguishesAbsentFromUnreachable(t *testing.T) {
	t.Run("no engine-manager falls back to the stock port", func(t *testing.T) {
		b := &Broker{}
		if got, ok := b.localMaxEnginePort(); !ok || got != defaultMaxPort {
			t.Fatalf("localMaxEnginePort = (%d, %v), want (%d, true)", got, ok, defaultMaxPort)
		}
	})

	t.Run("no MAX engine yields no port to probe", func(t *testing.T) {
		b := &Broker{}
		engine, engineCodec := newTestRPCWorkerPipe(t)
		b.setEngineMgr(engine)
		go func() {
			if msg, err := engineCodec.Read(); err == nil {
				_ = engineCodec.RespondError(msg.ID, -32000, `unknown engine "max"`)
			}
		}()
		if got, ok := b.localMaxEnginePort(); ok || got != 0 {
			t.Fatalf("localMaxEnginePort = (%d, %v), want (0, false)", got, ok)
		}
	})

	t.Run("a running MAX engine reports its port", func(t *testing.T) {
		b := &Broker{}
		engine, engineCodec := newTestRPCWorkerPipe(t)
		b.setEngineMgr(engine)
		go func() {
			if msg, err := engineCodec.Read(); err == nil {
				_ = engineCodec.Respond(msg.ID, ollamaPortStatus{Running: true, Port: managedMaxBackendStart})
			}
		}()
		if got, ok := b.localMaxEnginePort(); !ok || got != managedMaxBackendStart {
			t.Fatalf("localMaxEnginePort = (%d, %v), want (%d, true)", got, ok, managedMaxBackendStart)
		}
	})
}

// TestMaxFallbackNeverAdvertisesItsProxy mirrors the LM Studio guard: with the
// proxy sitting on the engine's stock port, the collision check must fire
// before any health probe, or the node advertises its own router as its engine
// and the ingress forwards to itself.
func TestMaxFallbackNeverAdvertisesItsProxy(t *testing.T) {
	proxyClient, proxyServer := net.Pipe()
	t.Cleanup(func() {
		_ = proxyClient.Close()
		_ = proxyServer.Close()
	})
	proxy := &proxyProcess{
		peer:  NewPeer(NewCodec(proxyClient)),
		ready: true,
		port:  defaultMaxPort,
	}
	go proxy.peer.Serve(nil, nil)

	localBackend := make(chan proxyLocalBackend, 1)
	go func() {
		codec := NewCodec(proxyServer)
		msg, err := codec.Read()
		if err != nil {
			return
		}
		var got proxyLocalBackend
		if json.Unmarshal(msg.Params, &got) == nil {
			localBackend <- got
		}
		_ = codec.Respond(msg.ID, map[string]bool{"ok": true})
	}()

	b := &Broker{regCache: relay.NewRegistrationCache()}
	b.setMaxProxy(proxy)

	// A nil client is intentional: collision detection must short-circuit
	// before any health request can mistake the proxy for the engine.
	b.reconcileAdvertiseMax(nil)
	if got := b.regCache.Snapshot(); len(got) != 0 {
		t.Fatalf("the MAX proxy was advertised as an engine: %+v", got)
	}
	select {
	case got := <-localBackend:
		if got.Port != 0 || got.Healthy {
			t.Fatalf("proxy listener was retained as the local backend: %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the MAX proxy did not receive a cleared local backend")
	}
}

// TestMaxFallbackDoesNotOverwriteKnownBackend pins that a stock-port guess is
// never promoted into the confirmed backend cache, whose authoritative owners
// are managed setup and live engine:status.
func TestMaxFallbackDoesNotOverwriteKnownBackend(t *testing.T) {
	b := &Broker{regCache: relay.NewRegistrationCache()}
	b.maxBackendPort.Store(managedMaxBackendStart)

	b.reconcileAdvertiseMax(nil)

	if got := int(b.maxBackendPort.Load()); got != managedMaxBackendStart {
		t.Fatalf("backend cache = %d, want %d (a fallback must not overwrite the confirmed backend)", got, managedMaxBackendStart)
	}
}

func TestMaxProxyListenPortNoProxy(t *testing.T) {
	b := &Broker{}
	if got := b.maxProxyListenPort(); got != 0 {
		t.Fatalf("no proxy: maxProxyListenPort = %d, want 0", got)
	}
}
