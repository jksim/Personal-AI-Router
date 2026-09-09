// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"strings"

	"nvpair-shared/applog"
	"nvpair-shared/noderec"
)

// maxproxy.go is the broker's MAX counterpart to its lmstudio-proxy wiring
// (lmstudioproxy.go). max-proxy is built from the same openai-proxy library as
// lmstudio-proxy and therefore speaks the identical JSON-RPC control plane —
// a "ready" port notification, node/add-manual/remove-manual, nodes/list,
// node/select, the workload:* lifecycle stream — so it reuses the proxyProcess
// client type. Only the namespace differs: the broker relays it under
// max-proxy: instead of lmstudio-proxy:.
//
// This is a deliberate duplication of the per-engine pattern rather than a
// generalisation of it. Each engine's wiring stays independently readable and
// independently rebasable against upstream, at the cost of one more copy.

func (b *Broker) setMaxProxy(p *proxyProcess) {
	b.workersMu.Lock()
	b.maxProxy = p
	b.workersMu.Unlock()
}

func (b *Broker) getMaxProxy() *proxyProcess {
	b.workersMu.Lock()
	defer b.workersMu.Unlock()
	return b.maxProxy
}

func (b *Broker) finishMaxProxyTerminal() {
	b.managedMaxFacade.Store(false)
	b.markMaxPortReady()
}

func (b *Broker) configureMaxProxySupervisorCallbacks(sup *supervisor) {
	sup.onCrash, sup.onRecovered = b.supervisedWorkerCallbacks("max-proxy", func() { b.setMaxProxy(nil) })
	sup.onExhausted = func(attempt int) {
		slog.Warn("max-proxy is terminally unavailable; releasing ownership gate", "attempt", attempt)
		b.finishMaxProxyTerminal()
	}
}

func (b *Broker) maxProxyArgs() []string {
	var args []string
	if port := int(b.maxProxyStartupPort.Load()); port != 0 {
		args = []string{"--port", fmt.Sprintf("%d", port), "--ignore-persisted-port"}
	}
	return append(args, b.clusterDirArgs()...)
}

// spawnMaxProxy is the max-proxy supervisor's spawn closure, mirroring
// spawnLMStudioProxy. It reuses startProxy because the two proxies share a
// binary protocol.
func (b *Broker) spawnMaxProxy() (supervisedHandle, error) {
	b.maxReadyMu.Lock()
	generation := b.maxProxyGeneration.Add(1)
	b.maxReadyMu.Unlock()
	// Thread the cluster dir so the MAX proxy brings up its pin-gated LAN mTLS
	// ingress (and dials peers over mTLS) once this node is clustered.
	pp, err := startProxy(
		"max-proxy",
		b.maxProxyPath,
		applog.LevelString(),
		b.relayDir,
		func(method string, params json.RawMessage) {
			b.forwardMaxProxyNotificationForGeneration(generation, method, params)
		},
		b.maxProxyArgs()...,
	)
	if err != nil {
		return nil, err
	}
	b.setMaxProxy(pp)
	b.maxProxyPublishedGeneration.Store(generation)
	// A fast child can announce ready before its handle is published. Replay
	// reconciliation after publication; the gate's sync.Once makes this safe
	// when the notification goroutine already handled it.
	if ready, port := pp.Status(); ready && port > 0 {
		go b.reconcileMaxProxyPortOnReadyForGeneration(generation, port)
	}
	slog.Info("max-proxy started", "path", b.maxProxyPath, "pid", pp.cmd.Process.Pid)
	return pp, nil
}

// forwardMaxProxyNotification is the hook startProxy invokes on the max-proxy
// reader goroutine. It mirrors forwardLMStudioProxyNotification: errors:report /
// errors:clear go into the nvpair-errors pipeline; workload lifecycle events are
// stamped and forwarded to the workload-manager for cluster broadcast (max-proxy
// tags its workloads "max"); everything else is re-emitted to max-proxy:
// subscribe'd clients as max-proxy:<method>. Readiness reconciliation runs on
// its own goroutine because its set-port/local-backend calls round-trip through
// this reader.
func (b *Broker) forwardMaxProxyNotification(method string, params json.RawMessage) {
	b.forwardMaxProxyNotificationForGeneration(b.maxProxyGeneration.Load(), method, params)
}

func (b *Broker) forwardMaxProxyNotificationForGeneration(generation uint64, method string, params json.RawMessage) {
	if b.maxProxyGeneration.Load() != generation {
		return
	}
	if b.dispatchErrorsNotif("max-proxy", method, params) {
		return
	}
	// A process can win :8000 after preparation's free-port check but before
	// the proxy binds. The failed process is exiting, so set the next spawn to
	// an explicit fallback without calling back into this reader goroutine.
	if method == "error" {
		var ep struct {
			Code string `json:"code"`
			Port int    `json:"port"`
		}
		if json.Unmarshal(params, &ep) == nil && ep.Code == "bind-failed" {
			if b.managedMaxFacade.Load() && ep.Port == managedMaxFacadePort {
				_, _ = b.blockManagedMaxFacade("another process acquired the compatibility port during startup", nil)
			} else {
				fallback := b.setMaxProxyFallback(ep.Port)
				slog.Warn("MAX proxy bind failed; retrying on fallback", "port", ep.Port, "fallback", fallback)
			}
		}
	}
	if proxyWorkloadMethods[method] {
		b.routeProxyWorkload(method, params)
		return
	}
	if method == noderec.NotifyNodeActivity {
		b.routeNodeActivity(params)
		return
	}
	if method == "ready" {
		var rp proxyReadyParams
		if err := json.Unmarshal(params, &rp); err == nil && rp.Port > 0 {
			go b.reconcileMaxProxyPortOnReadyForGeneration(generation, rp.Port)
		}
	}
	b.proxyMu.Lock()
	subscribed := b.maxProxySubscribed
	b.proxyMu.Unlock()
	if !subscribed {
		return
	}
	if err := b.codec.Notify("max-proxy:"+method, params); err != nil {
		slog.Warn("forward max-proxy notification failed", "method", method, "err", err)
	}
}

// relayToMaxProxy forwards a max-proxy:<method> request to max-proxy as
// <method> (prefix stripped) and maps its response straight back, mirroring
// relayToLMStudioProxy. max-proxy:shutdown is refused — the broker owns the
// proxy's lifecycle.
func (b *Broker) relayToMaxProxy(msg *Message) {
	method := strings.TrimPrefix(msg.Method, "max-proxy:")
	if method == "shutdown" {
		if err := b.codec.RespondError(msg.ID, -32601, "max-proxy:shutdown is not allowed; the broker owns the proxy lifecycle"); err != nil {
			log.Printf("failed to respond to max-proxy:shutdown: %v", err)
		}
		return
	}

	p := b.getMaxProxy()
	if p == nil {
		if err := b.codec.RespondError(msg.ID, -32000, "max-proxy not available"); err != nil {
			log.Printf("failed to respond to %s: %v", msg.Method, err)
		}
		return
	}

	result, rpcErr, err := p.Call(context.Background(), method, msg.Params)
	switch {
	case err != nil:
		if err := b.codec.RespondError(msg.ID, -32000, fmt.Sprintf("max-proxy call failed: %v", err)); err != nil {
			log.Printf("failed to respond to %s: %v", msg.Method, err)
		}
	case rpcErr != nil:
		if err := b.codec.RespondError(msg.ID, rpcErr.Code, rpcErr.Message); err != nil {
			log.Printf("failed to relay max-proxy error for %s: %v", msg.Method, err)
		}
	default:
		if err := b.codec.Respond(msg.ID, result); err != nil {
			log.Printf("failed to relay max-proxy result for %s: %v", msg.Method, err)
		}
	}
}
