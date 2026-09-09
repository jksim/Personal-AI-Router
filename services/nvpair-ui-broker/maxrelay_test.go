// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"
)

// brokerWithClient wires a broker's codec to a client end and returns the
// frames the broker writes back.
func brokerWithClient(t *testing.T) (*Broker, chan *Message) {
	t.Helper()
	brokerConn, clientConn := net.Pipe()
	t.Cleanup(func() {
		_ = brokerConn.Close()
		_ = clientConn.Close()
	})
	b := &Broker{codec: NewCodec(brokerConn)}
	responses := make(chan *Message, 4)
	go func() {
		codec := NewCodec(clientConn)
		for {
			msg, err := codec.Read()
			if err != nil {
				return
			}
			responses <- msg
		}
	}()
	return b, responses
}

func nextBrokerResponse(t *testing.T, responses chan *Message) *Message {
	t.Helper()
	select {
	case msg := <-responses:
		return msg
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a broker response")
		return nil
	}
}

// TestMaxProxyNamespaceIsNotSwallowedByProxy pins the namespace boundary.
//
// The default arm dispatches on prefix, and every engine proxy speaks the same
// method names. If "max-proxy:nodes/list" ever matched the "proxy:" arm it
// would be relayed to the Ollama proxy, which would answer it perfectly well
// with the wrong engine's nodes — a silent misroute, not an error. This asserts
// the request reaches no proxy at all when max-proxy is absent, which is only
// true if the MAX arm claimed it.
func TestMaxProxyNamespaceIsNotSwallowedByProxy(t *testing.T) {
	b, responses := brokerWithClient(t)
	ollamaClient, ollamaServer := net.Pipe()
	t.Cleanup(func() {
		_ = ollamaClient.Close()
		_ = ollamaServer.Close()
	})
	ollama := &proxyProcess{peer: NewPeer(NewCodec(ollamaClient))}
	go ollama.peer.Serve(nil, nil)
	b.setProxy(ollama)

	ollamaSaw := make(chan string, 1)
	go func() {
		if msg, err := NewCodec(ollamaServer).Read(); err == nil {
			ollamaSaw <- msg.Method
		}
	}()

	id := json.RawMessage(`1`)
	b.handleMessage(&Message{JSONRPC: "2.0", ID: &id, Method: "max-proxy:nodes/list"})

	msg := nextBrokerResponse(t, responses)
	if msg.Error == nil || !strings.Contains(msg.Error.Message, "max-proxy not available") {
		t.Fatalf("response = %+v, want a max-proxy-not-available error", msg)
	}
	select {
	case method := <-ollamaSaw:
		t.Fatalf("the Ollama proxy received %q; the MAX namespace was misrouted", method)
	case <-time.After(200 * time.Millisecond):
	}
}

// TestMaxProxyControlPlaneIsAnsweredLocally covers the four methods the broker
// answers itself rather than relaying, so a client can learn the proxy's port
// and opt into its event stream.
func TestMaxProxyControlPlaneIsAnsweredLocally(t *testing.T) {
	b, responses := brokerWithClient(t)
	proxyClient, proxyServer := net.Pipe()
	t.Cleanup(func() {
		_ = proxyClient.Close()
		_ = proxyServer.Close()
	})
	proxy := &proxyProcess{peer: NewPeer(NewCodec(proxyClient)), ready: true, port: managedMaxFacadePort}
	go proxy.peer.Serve(nil, nil)
	b.setMaxProxy(proxy)
	go func() {
		codec := NewCodec(proxyServer)
		for {
			msg, err := codec.Read()
			if err != nil {
				return
			}
			_ = codec.Respond(msg.ID, map[string]bool{"ok": true})
		}
	}()

	id := json.RawMessage(`1`)
	b.handleMessage(&Message{JSONRPC: "2.0", ID: &id, Method: "max-proxy:get-status"})
	var status ProxyStatusResult
	msg := nextBrokerResponse(t, responses)
	if msg.Error != nil || json.Unmarshal(msg.Result, &status) != nil {
		t.Fatalf("max-proxy:get-status response = %+v", msg)
	}
	if !status.Ready || status.Port != managedMaxFacadePort {
		t.Fatalf("status = %+v, want ready on %d", status, managedMaxFacadePort)
	}

	b.handleMessage(&Message{JSONRPC: "2.0", ID: &id, Method: "max-proxy:subscribe"})
	if msg := nextBrokerResponse(t, responses); msg.Error != nil {
		t.Fatalf("max-proxy:subscribe response = %+v", msg)
	}
	b.proxyMu.Lock()
	subscribed := b.maxProxySubscribed
	b.proxyMu.Unlock()
	if !subscribed {
		t.Fatal("max-proxy:subscribe did not record the subscription")
	}

	b.handleMessage(&Message{JSONRPC: "2.0", ID: &id, Method: "max-proxy:unsubscribe"})
	if msg := nextBrokerResponse(t, responses); msg.Error != nil {
		t.Fatalf("max-proxy:unsubscribe response = %+v", msg)
	}
	b.proxyMu.Lock()
	subscribed = b.maxProxySubscribed
	b.proxyMu.Unlock()
	if subscribed {
		t.Fatal("max-proxy:unsubscribe did not clear the subscription")
	}

	b.handleMessage(&Message{JSONRPC: "2.0", ID: &id, Method: "max-proxy:shutdown"})
	if msg := nextBrokerResponse(t, responses); msg.Error == nil || !strings.Contains(msg.Error.Message, "not allowed") {
		t.Fatalf("max-proxy:shutdown response = %+v, want a refusal", msg)
	}
}

// TestMaxProxySetPortRejectsTheOllamaHostAlias pins that max-proxy:set-port is
// intercepted rather than relayed. Binding the alias port would orphan the
// inherited OLLAMA_HOST alias, which no later reconcile recovers.
func TestMaxProxySetPortRejectsTheOllamaHostAlias(t *testing.T) {
	b, responses := brokerWithClient(t)
	b.setOllamaHostAlias(ollamaHostAlias{Port: 11433})

	id := json.RawMessage(`1`)
	b.handleMessage(&Message{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  "max-proxy:set-port",
		Params:  json.RawMessage(`{"port":11433}`),
	})
	msg := nextBrokerResponse(t, responses)
	if msg.Error == nil || !strings.Contains(msg.Error.Message, "OLLAMA_HOST proxy alias") {
		t.Fatalf("response = %+v, want an alias-port rejection", msg)
	}
}

// TestManagedMaxFacadePortIsRefusedAsABackend pins that the engine cannot be
// configured onto the port the managed proxy owns. Accepting it would leave the
// engine unable to bind and the router pointing at itself.
func TestManagedMaxFacadePortIsRefusedAsABackend(t *testing.T) {
	b, responses := brokerWithClient(t)
	b.maxPortReady = make(chan struct{})
	close(b.maxPortReady)
	b.managedMaxFacade.Store(true)

	id := json.RawMessage(`1`)
	b.handleMessage(&Message{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  "engine:set-port",
		Params:  json.RawMessage(`{"engine":"max","port":8000}`),
	})
	msg := nextBrokerResponse(t, responses)
	if msg.Error == nil || !strings.Contains(msg.Error.Message, "reserved by the managed MAX proxy") {
		t.Fatalf("response = %+v, want a reserved-port rejection", msg)
	}
}

// TestMaxEngineRequestsWaitForThePortGate pins that engine traffic for MAX is
// held while ownership of :8000 is still being resolved. A status probe that
// slipped through could adopt the proxy itself as the MAX engine.
func TestMaxEngineRequestsWaitForThePortGate(t *testing.T) {
	b, responses := brokerWithClient(t)
	b.maxPortReady = make(chan struct{})

	id := json.RawMessage(`1`)
	go b.handleMessage(&Message{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  "engine:status",
		Params:  json.RawMessage(`{"engine":"max"}`),
	})

	time.Sleep(200 * time.Millisecond)
	select {
	case msg := <-responses:
		t.Fatalf("engine:status was relayed before the gate opened: %+v", msg)
	default:
	}
	close(b.maxPortReady)

	// The gate released the request; with no engine-manager it now fails for
	// that reason rather than the gate's timeout.
	msg := nextBrokerResponse(t, responses)
	if msg.Error == nil || !strings.Contains(msg.Error.Message, "engine-manager not available") {
		t.Fatalf("response = %+v, want the request released past the gate", msg)
	}
}

// TestMaxBackendPortIsCachedFromASuccessfulSetPort pins that a successful
// engine:set-port updates the cached backend, so reconciliation does not move
// the proxy off a port the engine has just vacated.
func TestMaxBackendPortIsCachedFromASuccessfulSetPort(t *testing.T) {
	b, responses := brokerWithClient(t)
	b.maxPortReady = make(chan struct{})
	close(b.maxPortReady)

	engine, engineCodec := newTestRPCWorkerPipe(t)
	b.setEngineMgr(engine)
	go func() {
		if msg, err := engineCodec.Read(); err == nil {
			_ = engineCodec.Respond(msg.ID, ollamaPortStatus{Running: true, Port: 18000})
		}
	}()

	id := json.RawMessage(`1`)
	b.handleMessage(&Message{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  "engine:set-port",
		Params:  json.RawMessage(`{"engine":"max","port":18000}`),
	})
	if msg := nextBrokerResponse(t, responses); msg.Error != nil {
		t.Fatalf("engine:set-port response = %+v", msg)
	}
	if got := b.maxBackendPort.Load(); got != 18000 {
		t.Fatalf("cached MAX backend = %d, want 18000", got)
	}
}
