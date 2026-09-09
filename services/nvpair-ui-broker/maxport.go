// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"nvpair-shared/errors"
)

const (
	// MAX serves on 8000 by default, so the facade squats it and the engine is
	// displaced to 8001 — the same shape as Ollama (11434/11435) and LM Studio
	// (1234/1235). A client that points at the engine's canonical port reaches
	// the router, which is the whole point of the facade.
	managedMaxFacadePort      = 8000
	managedMaxBackendStart    = 8001
	defaultMaxPort            = managedMaxFacadePort
	maxPortOwnershipBlockedID = "max-proxy:port-ownership-blocked"
)

// planManagedMaxPorts mirrors planManagedLMStudioPorts. MAX is started and
// stopped by engine-manager as an identified command-mode runtime, so a move is
// safe under the same conditions: process-mode and unknown owners are still
// refused by engine:set-port.
func planManagedMaxPorts(enabled bool, st ollamaPortStatus, available func(int) bool) managedPortPlan {
	if !enabled {
		return managedPortPlan{}
	}
	if st.Port > 0 && st.Port != managedMaxFacadePort {
		if !available(managedMaxFacadePort) {
			return managedPortPlan{Blocked: "the compatibility port is already in use"}
		}
		if !st.Running && !available(st.Port) {
			backend := nextAvailablePort(managedMaxBackendStart, available)
			if backend == 0 {
				return managedPortPlan{Blocked: "no free backend port is available"}
			}
			return managedPortPlan{Enabled: true, BackendPort: backend}
		}
		return managedPortPlan{Enabled: true}
	}
	backend := nextAvailablePort(managedMaxBackendStart, available)
	if backend == 0 {
		return managedPortPlan{Blocked: "no free backend port is available"}
	}
	return managedPortPlan{Enabled: true, BackendPort: backend}
}

func (b *Broker) markMaxPortReady() {
	if b.maxPortReady != nil {
		b.maxPortReadyOnce.Do(func() { close(b.maxPortReady) })
	}
}

func (b *Broker) maxPortOwnershipPending() bool {
	if b.maxPortReady == nil {
		return false
	}
	select {
	case <-b.maxPortReady:
		return false
	default:
		return true
	}
}

func needsMaxPortGate(method string, params json.RawMessage) bool {
	if method == "engine:get-installed" {
		return true
	}
	if method != "engine:status" && method != "engine:start" && method != "engine:restart" {
		return false
	}
	var request struct {
		Engine string `json:"engine"`
	}
	return json.Unmarshal(params, &request) == nil && request.Engine == "max"
}

func maxSetPortRequest(method string, params json.RawMessage) (int, bool) {
	if method != "engine:set-port" {
		return 0, false
	}
	var request struct {
		Engine string `json:"engine"`
		Port   int    `json:"port"`
	}
	if json.Unmarshal(params, &request) != nil || request.Engine != "max" || request.Port <= 0 {
		return 0, false
	}
	return request.Port, true
}

func (b *Broker) reportMaxPortOwnershipBlocked(reason string) {
	b.forwardErrorsReport(errors.ServiceError{
		ID:        maxPortOwnershipBlockedID,
		Message:   fmt.Sprintf("NVPAIR could not safely reserve MAX port %d: %s. No unknown process was stopped.", managedMaxFacadePort, reason),
		Timestamp: nowMillis(),
		NodeID:    b.nodeID,
		Severity:  "warning",
		Action:    "none",
	})
}

func (b *Broker) setMaxProxyFallback(excludedPorts ...int) int {
	if aliasPort := b.currentOllamaHostAlias().Port; aliasPort > 0 {
		excludedPorts = append(excludedPorts, aliasPort)
	}
	if backend := int(b.maxBackendPort.Load()); backend > 0 {
		excludedPorts = append(excludedPorts, backend)
	} else {
		// With no authoritative backend yet, neither MAX's compatibility port
		// nor engine-manager's bundled backend default is safe evidence of a
		// free proxy port.
		excludedPorts = append(excludedPorts, managedMaxFacadePort, managedMaxBackendStart)
	}
	fallback := nextAvailablePortExcluding(managedMaxBackendStart, excludedPorts, tcpPortAvailable)
	b.maxProxyStartupPort.Store(int32(fallback))
	return fallback
}

func (b *Broker) rebindMaxProxy(p *proxyProcess, port int) bool {
	if p == nil || port == 0 {
		return false
	}
	body, _ := json.Marshal(map[string]int{"port": port})
	result, rpcErr, err := p.Call(context.Background(), "set-port", body)
	if err != nil || rpcErr != nil {
		slog.Warn("failed to rebind MAX proxy", "port", port, "err", err, "rpcErr", rpcErr)
		return false
	}
	var ready proxyReadyParams
	return json.Unmarshal(result, &ready) == nil && ready.Port == port
}

// blockManagedMaxFacade records an explicit fallback and optionally rebinds a
// live proxy. It does not open the ownership gate: only a confirmed bound proxy
// generation or an exhausted supervisor may do that.
func (b *Broker) blockManagedMaxFacade(reason string, p *proxyProcess, excludedPorts ...int) (int, bool) {
	b.managedMaxFacade.Store(false)
	fallback := b.setMaxProxyFallback(excludedPorts...)
	b.reportMaxPortOwnershipBlocked(reason)
	return fallback, b.rebindMaxProxy(p, fallback)
}

func (b *Broker) cacheMaxPortStatus() (ollamaPortStatus, bool) {
	em := b.getEngineMgr()
	if em == nil {
		return ollamaPortStatus{}, false
	}
	params, _ := json.Marshal(map[string]string{"engine": "max"})
	result, rpcErr, err := em.Call(context.Background(), "engine:status", params)
	if err != nil || rpcErr != nil {
		return ollamaPortStatus{}, false
	}
	var st ollamaPortStatus
	if json.Unmarshal(result, &st) != nil {
		return ollamaPortStatus{}, false
	}
	if st.Port <= 0 {
		return st, false
	}
	b.maxBackendPort.Store(int32(st.Port))
	return st, true
}

func (b *Broker) configureUnmanagedMaxFacade() {
	b.managedMaxFacade.Store(false)
	b.maxProxyStartupPort.Store(0)
	b.forwardErrorsClear(maxPortOwnershipBlockedID)
}

// engineManagerKnowsMax reports whether engine-manager has a MAX engine at all.
//
// This check has no counterpart in the Ollama or LM Studio wiring because both
// of those engines are always in engine-manager's registry. MAX is not: the
// router can front a MAX engine that some other tool is running, and on a host
// where engine-manager has no MAX manifest every engine:status for it is an
// error. Treating that error as "status could not be verified" would report a
// port-ownership warning on a host that has nothing wrong with it, and would
// leave the ownership gate closed forever because no backend port ever arrives.
// So the absence of the engine is detected explicitly and separated from a
// genuine engine-manager failure.
//
// The bool is only meaningful when error is nil.
func (b *Broker) engineManagerKnowsMax() (bool, error) {
	em := b.getEngineMgr()
	if em == nil {
		return false, fmt.Errorf("engine manager is unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), rpcWorkerCallTimeout)
	defer cancel()
	result, rpcErr, err := em.Call(ctx, "engine:get-installed", nil)
	if err != nil {
		return false, err
	}
	if rpcErr != nil {
		return false, fmt.Errorf("engine:get-installed: %s", rpcErr.Message)
	}
	var response struct {
		Engines []struct {
			Engine string `json:"engine"`
		} `json:"engines"`
	}
	if err := json.Unmarshal(result, &response); err != nil {
		return false, fmt.Errorf("decode engine:get-installed: %w", err)
	}
	for _, engine := range response.Engines {
		if engine.Engine == "max" {
			return true, nil
		}
	}
	return false, nil
}

// prepareManagedMaxFacade runs after engine-manager starts and before the MAX
// proxy is spawned. It mirrors prepareManagedLMStudioFacade: the backend move
// happens first, and only after :8000 is verified free does the broker force
// the proxy onto the compatibility port.
func (b *Broker) prepareManagedMaxFacade() {
	b.prepareManagedMaxFacadeWithPortCheck(tcpPortAvailable)
}

func (b *Broker) prepareManagedMaxFacadeWithPortCheck(portAvailable func(int) bool) {
	// Ollama's facade is prepared first, so an inherited OLLAMA_HOST alias is
	// already reserved here and must stay out of MAX's backend search.
	portAvailable = b.availableOffOllamaHostAlias(portAvailable)
	settings := b.getSettings()
	if settings == nil {
		b.cacheMaxPortStatus()
		_, _ = b.blockManagedMaxFacade("managed-port policy is unavailable", nil)
		return
	}
	result, rpcErr, err := settings.Call(context.Background(), "settings/get-force-ports", nil)
	if err != nil || rpcErr != nil {
		slog.Warn("failed to read managed MAX port setting", "err", err, "rpcErr", rpcErr)
		b.cacheMaxPortStatus()
		_, _ = b.blockManagedMaxFacade("managed-port policy could not be verified", nil)
		return
	}
	var policy struct {
		Value bool `json:"value"`
	}
	if json.Unmarshal(result, &policy) != nil {
		b.cacheMaxPortStatus()
		_, _ = b.blockManagedMaxFacade("managed-port policy could not be decoded", nil)
		return
	}

	em := b.getEngineMgr()
	if em == nil {
		if !policy.Value {
			b.configureUnmanagedMaxFacade()
			return
		}
		_, _ = b.blockManagedMaxFacade("engine manager is unavailable", nil)
		return
	}

	// A host with no MAX engine has nothing to move and nothing to collide
	// with, so the proxy simply takes its own default port. This is a normal
	// configuration, not a failure, and must not report a warning.
	known, err := b.engineManagerKnowsMax()
	if err != nil {
		if !policy.Value {
			b.configureUnmanagedMaxFacade()
			return
		}
		_, _ = b.blockManagedMaxFacade("the installed engines could not be listed", nil)
		return
	}
	if !known {
		slog.Info("engine-manager has no MAX engine; running the MAX proxy unmanaged")
		b.configureUnmanagedMaxFacade()
		return
	}
	b.maxEngineKnown.Store(true)

	params, _ := json.Marshal(map[string]any{"engine": "max", "port": managedMaxFacadePort})
	result, rpcErr, err = em.Call(context.Background(), "engine:status", params)
	if err != nil || rpcErr != nil {
		if !policy.Value {
			b.configureUnmanagedMaxFacade()
			return
		}
		_, _ = b.blockManagedMaxFacade("MAX status could not be verified", nil)
		return
	}
	var st ollamaPortStatus
	if json.Unmarshal(result, &st) != nil {
		if !policy.Value {
			b.configureUnmanagedMaxFacade()
			return
		}
		_, _ = b.blockManagedMaxFacade("MAX status could not be decoded", nil)
		return
	}
	if st.Port > 0 {
		b.maxBackendPort.Store(int32(st.Port))
	}
	if !policy.Value {
		b.configureUnmanagedMaxFacade()
		return
	}

	plan := planManagedMaxPorts(true, st, portAvailable)
	if plan.Blocked != "" {
		_, _ = b.blockManagedMaxFacade(plan.Blocked, nil, st.Port)
		return
	}
	if plan.BackendPort != 0 {
		params, _ = json.Marshal(map[string]any{"engine": "max", "port": plan.BackendPort})
		result, rpcErr, err = em.CallNoTimeout(context.Background(), "engine:set-port", params)
		if err != nil || rpcErr != nil {
			b.cacheMaxPortStatus()
			_, _ = b.blockManagedMaxFacade("the MAX backend could not be moved", nil, st.Port, plan.BackendPort)
			return
		}
		b.maxBackendPort.Store(int32(plan.BackendPort))
		var moved ollamaPortStatus
		if json.Unmarshal(result, &moved) == nil && moved.Port > 0 {
			b.maxBackendPort.Store(int32(moved.Port))
		}
	}
	if !portAvailable(managedMaxFacadePort) {
		_, _ = b.blockManagedMaxFacade("the compatibility port is already in use", nil, st.Port)
		return
	}

	b.managedMaxFacade.Store(plan.Enabled)
	b.maxProxyStartupPort.Store(managedMaxFacadePort)
	b.forwardErrorsClear(maxPortOwnershipBlockedID)
}

func (b *Broker) maxProxyGenerationIsCurrent(generation uint64, p *proxyProcess) bool {
	return b.maxProxyGeneration.Load() == generation &&
		b.maxProxyPublishedGeneration.Load() == generation &&
		b.getMaxProxy() == p
}

// invalidateMaxProxyForRestartLocked invalidates the current logical generation
// and broker-visible handle. Caller holds maxReadyMu.
func (b *Broker) invalidateMaxProxyForRestartLocked(generation uint64) bool {
	if b.maxProxyGeneration.Load() != generation {
		return false
	}
	b.maxProxyGeneration.Add(1)
	b.maxProxyPublishedGeneration.Store(0)
	b.setMaxProxy(nil)
	return true
}

func (b *Broker) restartMaxProxyOrFinish(generation uint64) {
	// Caller holds maxReadyMu. Invalidate before the restart request becomes
	// observable so a ready notification already queued by the failed process
	// cannot complete the gate.
	if !b.invalidateMaxProxyForRestartLocked(generation) {
		return
	}
	if b.maxProxySup != nil {
		b.maxProxySup.Restart()
		return
	}
	b.finishMaxProxyTerminal()
}

func (b *Broker) reconcileMaxProxyAfterEngineManagerReady() {
	if !b.maxPortOwnershipPending() {
		return
	}
	p := b.getMaxProxy()
	if p == nil {
		return
	}
	ready, port := p.Status()
	generation := b.maxProxyPublishedGeneration.Load()
	if !ready || port <= 0 || generation != b.maxProxyGeneration.Load() {
		return
	}
	go b.reconcileMaxProxyPortOnReadyForGeneration(generation, port)
}

// reconcileMaxProxyPortOnReady runs off the proxy reader goroutine because both
// set-port and node/set-local-backend round-trip through that reader.
func (b *Broker) reconcileMaxProxyPortOnReady(boundPort int) {
	b.reconcileMaxProxyPortOnReadyForGeneration(b.maxProxyGeneration.Load(), boundPort)
}

func (b *Broker) reconcileMaxProxyPortOnReadyForGeneration(generation uint64, boundPort int) {
	if b.maxProxyGeneration.Load() != generation {
		return
	}
	// Serialize the ownership transition, including fallback rebind. A set-port
	// emits another ready before its response, so without this guard that second
	// callback could release the gate while the first callback was still moving
	// the proxy. No caller holds another broker lock when entering this method.
	b.maxReadyMu.Lock()
	defer b.maxReadyMu.Unlock()

	p := b.getMaxProxy()
	if p == nil || !b.maxProxyGenerationIsCurrent(generation, p) {
		return
	}
	if b.managedMaxFacade.Load() && boundPort != managedMaxFacadePort {
		if b.rebindMaxProxy(p, managedMaxFacadePort) {
			boundPort = managedMaxFacadePort
		} else {
			fallback, rebound := b.blockManagedMaxFacade("the proxy could not bind the compatibility port", p, boundPort)
			if !rebound {
				b.restartMaxProxyOrFinish(generation)
				return
			}
			boundPort = fallback
		}
	}

	// With no MAX engine under engine-manager there is no backend to collide
	// with and none to report, and no engine:status will ever supply one. Open
	// the gate on whatever port the proxy bound rather than waiting for a
	// backend that cannot arrive.
	if !b.maxEngineKnown.Load() {
		b.setProxyLocalBackend(p, "max", 0, false)
		if !b.maxProxyGenerationIsCurrent(generation, p) {
			return
		}
		b.markMaxPortReady()
		b.repushPriority("max")
		return
	}

	backend := int(b.maxBackendPort.Load())
	if backend == 0 {
		// Fail closed on the bundled backend default before asking
		// engine-manager for status. Its identity probe is also rejected by the
		// facade, but moving first avoids even transiently probing this proxy.
		if boundPort == managedMaxBackendStart {
			fallback := b.setMaxProxyFallback(boundPort)
			if !b.rebindMaxProxy(p, fallback) {
				b.restartMaxProxyOrFinish(generation)
				return
			}
			boundPort = fallback
		}
		backend = defaultMaxPort
	}
	avoidBackendCollision := func() bool {
		if backend != boundPort {
			return true
		}
		var fallback int
		var rebound bool
		if b.managedMaxFacade.Load() {
			fallback, rebound = b.blockManagedMaxFacade("the proxy bound the configured MAX backend port", p, boundPort)
		} else {
			fallback = b.setMaxProxyFallback(boundPort)
			rebound = b.rebindMaxProxy(p, fallback)
		}
		if !rebound {
			b.restartMaxProxyOrFinish(generation)
			return false
		}
		boundPort = fallback
		return true
	}
	// Never probe engine-manager while the proxy is sitting on the configured
	// backend: that probe could adopt the proxy as MAX. Move the proxy first,
	// then refresh authoritative engine state.
	if !avoidBackendCollision() {
		return
	}

	st, current := b.cacheMaxPortStatus()
	if !b.maxProxyGenerationIsCurrent(generation, p) {
		return
	}
	cached := int(b.maxBackendPort.Load())
	if cached <= 0 {
		// A bound proxy plus an unknown configured backend is not a terminal
		// ownership result. Keep restoration gated until engine-manager (or a
		// replacement manager) supplies the authoritative port.
		slog.Warn("MAX backend port remains unknown; keeping ownership gate closed")
		return
	}
	backend = cached
	if !avoidBackendCollision() {
		return
	}
	healthy := current && st.Running && st.Port == backend
	if !b.maxProxyGenerationIsCurrent(generation, p) {
		return
	}
	b.setProxyLocalBackend(p, "max", backend, healthy)
	if !b.maxProxyGenerationIsCurrent(generation, p) {
		return
	}
	b.markMaxPortReady()
	b.repushPriority("max")
}
