// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"nvpair-shared/noderec"
)

// runAutoAdvertiseMax is the MAX sibling of runAutoAdvertiseLMStudio: it polls
// the local MAX server and reconciles this node's mx service registration
// against it, so a MAX host appears on the cluster the same way an Ollama or LM
// Studio host does. Kept parallel to those loops rather than folded into them,
// for the same reason they are kept parallel to each other.
func (b *Broker) runAutoAdvertiseMax(ctx context.Context) {
	client := &http.Client{Timeout: 2 * time.Second}
	ticker := time.NewTicker(autoAdvertiseInterval)
	defer ticker.Stop()

	b.reconcileAdvertiseMax(client)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			b.reconcileAdvertiseMax(client)
		}
	}
}

// reconcileAdvertiseMax brings this node's mx registration into line with the
// local MAX server, mirroring reconcileAdvertiseLMStudio: it advertises the
// promoted proxy port (never the engine) and hands the engine's loopback port to
// the MAX proxy via node/set-local-backend.
func (b *Broker) reconcileAdvertiseMax(client *http.Client) {
	enginePort, probe := b.localMaxEnginePort()
	proxyPort := b.maxProxyListenPort()
	if proxyPort != 0 && enginePort == proxyPort {
		// engine-manager may be temporarily unavailable after managed setup.
		// Prefer the last confirmed backend, but never hand the proxy its own
		// listener as a local destination.
		if cached := int(b.maxBackendPort.Load()); cached > 0 && cached != proxyPort {
			enginePort = cached
		} else {
			enginePort = 0
			probe = false
		}
	}
	// enginePort may be a stock-port fallback, usable for this tick only. It is
	// never written back to maxBackendPort, whose authoritative owners are the
	// managed facade setup and live engine:status — promoting a guess there
	// would later make the facade proxy look like the backend.
	up := probe && proxyPort != 0 && enginePort != proxyPort && checkMaxHealth(client, enginePort)
	if up {
		b.registerService(noderec.RegisterParams{Service: noderec.ServiceMax, Port: proxyPort})
		b.setProxyLocalBackend(b.getMaxProxy(), "max", enginePort, true)
	} else {
		b.unregisterService(noderec.ServiceMax)
		b.setProxyLocalBackend(b.getMaxProxy(), "max", enginePort, false)
	}
}

// localMaxEnginePort resolves the MAX engine's port for one advertise tick.
//
// It departs from localEnginePort in the one way that matters for an engine
// that need not be in engine-manager's registry at all: an RPC error means
// "engine-manager has no MAX engine", and must not fall back to the stock port.
// The fallback exists for a manager that cannot answer, and applying it here
// would adopt any OpenAI-compatible server that happened to answer on 8000 as
// this node's MAX engine — advertising someone else's server to the cluster.
// Only an absent or unreachable manager keeps the fallback, which is the case
// where there is no information either way.
//
// The bool says whether there is a port worth probing.
func (b *Broker) localMaxEnginePort() (int, bool) {
	em := b.getEngineMgr()
	if em == nil {
		return defaultMaxPort, true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	params, _ := json.Marshal(map[string]string{"engine": "max"})
	result, rpcErr, err := em.Call(ctx, "engine:status", params)
	if err != nil {
		return defaultMaxPort, true
	}
	if rpcErr != nil {
		return 0, false
	}
	return runningEnginePort(result)
}

// maxProxyListenPort is the MAX sibling of lmstudioProxyListenPort. It prevents
// the compatibility fallback from mistaking a proxy sitting on :8000 for the
// actual engine.
func (b *Broker) maxProxyListenPort() int {
	if p := b.getMaxProxy(); p != nil {
		if ready, port := p.Status(); ready {
			return port
		}
	}
	return 0
}

// checkMaxHealth reports whether a local MAX server is answering on the given
// port. MAX serves the OpenAI-compatible API, so a 200 from /v1/models is its
// liveness signal. The port is resolved per poll, never hardcoded, so the proxy
// is not mistaken for the engine.
func checkMaxHealth(client *http.Client, port int) bool {
	resp, err := client.Get(fmt.Sprintf("http://localhost:%d/v1/models", port))
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
