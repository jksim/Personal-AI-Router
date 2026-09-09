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

func TestPlanManagedMaxPorts(t *testing.T) {
	free := func(ports ...int) func(int) bool {
		set := map[int]bool{}
		for _, port := range ports {
			set[port] = true
		}
		return func(port int) bool { return set[port] }
	}
	tests := []struct {
		name string
		on   bool
		st   ollamaPortStatus
		free func(int) bool
		want managedPortPlan
	}{
		{"disabled", false, ollamaPortStatus{Port: 8000}, free(8000, 8001), managedPortPlan{}},
		{"stopped default moves", true, ollamaPortStatus{Port: 8000}, free(8000, 8001), managedPortPlan{Enabled: true, BackendPort: 8001}},
		{"running identified default moves", true, ollamaPortStatus{Running: true, Port: 8000}, free(8001), managedPortPlan{Enabled: true, BackendPort: 8001}},
		{"occupied stopped backend advances", true, ollamaPortStatus{Port: 8001}, free(8000, 8002), managedPortPlan{Enabled: true, BackendPort: 8002}},
		{"running backend is preserved", true, ollamaPortStatus{Running: true, Port: 8001}, free(8000, 8002), managedPortPlan{Enabled: true}},
		{"custom backend preserved", true, ollamaPortStatus{Running: true, Port: 18000}, free(8000), managedPortPlan{Enabled: true}},
		{"unknown facade owner blocks", true, ollamaPortStatus{Port: 8001}, free(8001), managedPortPlan{Blocked: "the compatibility port is already in use"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := planManagedMaxPorts(tc.on, tc.st, tc.free); got != tc.want {
				t.Fatalf("planManagedMaxPorts() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestManagedMaxProxyStartupArgs(t *testing.T) {
	b := &Broker{}
	b.maxProxyStartupPort.Store(managedMaxFacadePort)
	want := []string{"--port", "8000", "--ignore-persisted-port"}
	if got := b.maxProxyArgs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("managed startup args = %v, want %v", got, want)
	}

	b.maxProxyStartupPort.Store(0)
	if got := b.maxProxyArgs(); len(got) != 0 {
		t.Fatalf("opt-out startup args = %v, want persisted-port behavior", got)
	}
}

// TestMaxPortGateRequestMatcher pins that the gate and the set-port sniffer key
// on this engine alone. Matching another engine's traffic would make one
// engine's requests wait on a different engine's ownership gate, and would let
// a set-port for another engine overwrite the cached MAX backend port.
func TestMaxPortGateRequestMatcher(t *testing.T) {
	gateCases := []struct {
		name   string
		method string
		params string
		want   bool
	}{
		{"get-installed always gates", "engine:get-installed", `null`, true},
		{"max status gates", "engine:status", `{"engine":"max"}`, true},
		{"max start gates", "engine:start", `{"engine":"max"}`, true},
		{"max restart gates", "engine:restart", `{"engine":"max"}`, true},
		{"another engine does not gate", "engine:status", `{"engine":"lmstudio"}`, false},
		{"unrelated method does not gate", "engine:stop", `{"engine":"max"}`, false},
		{"unparseable params do not gate", "engine:status", `not json`, false},
	}
	for _, tc := range gateCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := needsMaxPortGate(tc.method, json.RawMessage(tc.params)); got != tc.want {
				t.Fatalf("needsMaxPortGate(%q, %s) = %v, want %v", tc.method, tc.params, got, tc.want)
			}
		})
	}

	setPortCases := []struct {
		name     string
		method   string
		params   string
		wantPort int
		wantOK   bool
	}{
		{"max set-port", "engine:set-port", `{"engine":"max","port":18000}`, 18000, true},
		{"another engine ignored", "engine:set-port", `{"engine":"ollama","port":18000}`, 0, false},
		{"non-positive port ignored", "engine:set-port", `{"engine":"max","port":0}`, 0, false},
		{"unrelated method ignored", "engine:status", `{"engine":"max","port":18000}`, 0, false},
	}
	for _, tc := range setPortCases {
		t.Run(tc.name, func(t *testing.T) {
			port, ok := maxSetPortRequest(tc.method, json.RawMessage(tc.params))
			if port != tc.wantPort || ok != tc.wantOK {
				t.Fatalf("maxSetPortRequest(%q, %s) = (%d, %v), want (%d, %v)",
					tc.method, tc.params, port, ok, tc.wantPort, tc.wantOK)
			}
		})
	}
}

func TestManagedMaxReadyOpensGateAndPushesBackend(t *testing.T) {
	engineClient, engineServer := net.Pipe()
	proxyClient, proxyServer := net.Pipe()
	t.Cleanup(func() {
		_ = engineClient.Close()
		_ = engineServer.Close()
		_ = proxyClient.Close()
		_ = proxyServer.Close()
	})

	engine := &rpcWorker{peer: NewPeer(NewCodec(engineClient))}
	go engine.peer.Serve(nil, nil)
	proxy := &proxyProcess{peer: NewPeer(NewCodec(proxyClient))}
	go proxy.peer.Serve(nil, nil)

	b := &Broker{maxPortReady: make(chan struct{})}
	b.managedMaxFacade.Store(true)
	b.maxEngineKnown.Store(true)
	b.maxBackendPort.Store(managedMaxBackendStart)
	b.setEngineMgr(engine)
	b.setMaxProxy(proxy)

	engineMethod := make(chan string, 1)
	go func() {
		codec := NewCodec(engineServer)
		msg, err := codec.Read()
		if err != nil {
			return
		}
		engineMethod <- msg.Method
		_ = codec.Respond(msg.ID, ollamaPortStatus{Running: true, Port: managedMaxBackendStart})
	}()

	backend := make(chan proxyLocalBackend, 1)
	go func() {
		codec := NewCodec(proxyServer)
		msg, err := codec.Read()
		if err != nil {
			return
		}
		var got proxyLocalBackend
		if json.Unmarshal(msg.Params, &got) == nil {
			backend <- got
		}
		_ = codec.Respond(msg.ID, map[string]bool{"ok": true})
	}()

	b.forwardMaxProxyNotification("ready", json.RawMessage(`{"version":"test","port":8000}`))

	select {
	case <-b.maxPortReady:
	case <-time.After(2 * time.Second):
		t.Fatal("MAX ownership gate did not open")
	}
	select {
	case got := <-engineMethod:
		if got != "engine:status" {
			t.Fatalf("engine method = %q, want engine:status", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("MAX ready did not refresh engine status")
	}
	select {
	case got := <-backend:
		if got.Engine != "max" || got.Port != managedMaxBackendStart || !got.Healthy {
			t.Fatalf("local backend = %+v, want healthy max:%d", got, managedMaxBackendStart)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("MAX ready did not push the local backend")
	}
}

// TestUnknownMaxEngineOpensGateWithoutBackend covers the one place this wiring
// deliberately departs from LM Studio's. When engine-manager has no MAX engine
// there is no backend port and none will ever arrive, so reconciliation must
// clear the proxy's local backend and open the gate on the bound port. Waiting
// for a status that cannot come would keep engine requests blocked forever.
func TestUnknownMaxEngineOpensGateWithoutBackend(t *testing.T) {
	engineClient, engineServer := net.Pipe()
	proxyClient, proxyServer := net.Pipe()
	t.Cleanup(func() {
		_ = engineClient.Close()
		_ = engineServer.Close()
		_ = proxyClient.Close()
		_ = proxyServer.Close()
	})

	engine := &rpcWorker{peer: NewPeer(NewCodec(engineClient))}
	go engine.peer.Serve(nil, nil)
	proxy := &proxyProcess{peer: NewPeer(NewCodec(proxyClient))}
	go proxy.peer.Serve(nil, nil)

	b := &Broker{maxPortReady: make(chan struct{})}
	b.setEngineMgr(engine)
	b.setMaxProxy(proxy)

	engineCalled := make(chan string, 1)
	go func() {
		codec := NewCodec(engineServer)
		if msg, err := codec.Read(); err == nil {
			engineCalled <- msg.Method
			_ = codec.Respond(msg.ID, ollamaPortStatus{})
		}
	}()

	backend := make(chan proxyLocalBackend, 1)
	go func() {
		codec := NewCodec(proxyServer)
		msg, err := codec.Read()
		if err != nil {
			return
		}
		var got proxyLocalBackend
		if json.Unmarshal(msg.Params, &got) == nil {
			backend <- got
		}
		_ = codec.Respond(msg.ID, map[string]bool{"ok": true})
	}()

	b.forwardMaxProxyNotification("ready", json.RawMessage(`{"version":"test","port":8000}`))

	select {
	case <-b.maxPortReady:
	case <-time.After(2 * time.Second):
		t.Fatal("MAX ownership gate stayed closed with no MAX engine present")
	}
	select {
	case got := <-backend:
		if got.Engine != "max" || got.Port != 0 || got.Healthy {
			t.Fatalf("local backend = %+v, want a cleared max backend", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("MAX ready did not clear the local backend")
	}
	select {
	case method := <-engineCalled:
		t.Fatalf("engine-manager was probed (%s) for an engine it does not have", method)
	default:
	}
}

// TestPrepareMaxFacadeWithoutMaxEngineRunsUnmanaged pins that a host whose
// engine-manager has no MAX engine is a normal configuration: the proxy keeps
// its own default port and no port-ownership warning is raised. Reporting one
// would put a permanent warning on every host that routes to a MAX engine it
// does not itself manage.
func TestPrepareMaxFacadeWithoutMaxEngineRunsUnmanaged(t *testing.T) {
	settings, settingsCodec := newTestRPCWorkerPipe(t)
	engine, engineCodec := newTestRPCWorkerPipe(t)
	b := &Broker{maxPortReady: make(chan struct{})}
	b.setSettings(settings)
	b.setEngineMgr(engine)

	go func() {
		if msg, err := settingsCodec.Read(); err == nil {
			_ = settingsCodec.Respond(msg.ID, map[string]bool{"value": true})
		}
	}()
	engineMethods := make(chan string, 2)
	go func() {
		for {
			msg, err := engineCodec.Read()
			if err != nil {
				return
			}
			engineMethods <- msg.Method
			_ = engineCodec.Respond(msg.ID, map[string]any{"engines": []map[string]any{
				{"engine": "ollama", "port": 11434},
				{"engine": "lmstudio", "port": 1234},
			}})
		}
	}()

	b.prepareManagedMaxFacadeWithPortCheck(func(int) bool { return true })

	select {
	case got := <-engineMethods:
		if got != "engine:get-installed" {
			t.Fatalf("first engine call = %q, want engine:get-installed", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("preparation did not ask engine-manager which engines exist")
	}
	select {
	case got := <-engineMethods:
		t.Fatalf("preparation made a second engine call (%s) for an absent engine", got)
	case <-time.After(200 * time.Millisecond):
	}

	if b.maxEngineKnown.Load() {
		t.Fatal("preparation recorded a MAX engine engine-manager does not have")
	}
	if b.managedMaxFacade.Load() {
		t.Fatal("preparation enabled managed ownership with no MAX engine")
	}
	if got := b.maxProxyStartupPort.Load(); got != 0 {
		t.Fatalf("proxy startup port = %d, want the proxy's own default", got)
	}
}
