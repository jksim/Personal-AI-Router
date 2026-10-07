// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"

	"nvpair-shared/noderec"
	"nvpair-ui-broker/relay"
	"strings"
	"testing"
)

// TestEveryEngineCanHaveItsFacadeEnabled covers the defect a live routing test
// found: enableEngineFacade was a switch over two engines, so a third was
// refused with "no facade spec for engine". The proxy then exited because no
// facade had come up, and the engine looked absent rather than broken.
//
// This asserts over the whole engine table rather than naming an engine, so it
// fails when an engine is added without a spec instead of after a timeout in a
// live test.
func TestEveryEngineCanHaveItsFacadeEnabled(t *testing.T) {
	for _, profile := range engineProxyProfiles {
		b := &Broker{}
		// A nil proxyProcess reaches the request-sending step and fails there,
		// which is past the lookup this test is about. What must not happen is
		// a refusal naming the engine as unknown.
		err := b.enableEngineFacade(context.Background(), nil, profile, ollamaHostAlias{})
		if err != nil && strings.Contains(err.Error(), "no facade spec") {
			t.Errorf("engine %q has no facade spec: %v", profile.Name, err)
		}
	}
}

// TestEngineFacadeSpecUsesThePlannedPort checks the generic spec carries the
// broker's planned port and tells the proxy to ignore its persisted one.
func TestEngineFacadeSpecUsesThePlannedPort(t *testing.T) {
	for _, profile := range engineProxyProfiles {
		b := &Broker{}
		if spec := b.engineFacadeSpec(profile); spec.Engine != profile.Name || spec.Port != 0 || spec.IgnorePersistedPort {
			t.Errorf("%s: with nothing planned got %+v, want the proxy's persisted port left alone", profile.Name, spec)
		}
		b.engineProxy(profile).startupPort.Store(9876)
		spec := b.engineFacadeSpec(profile)
		if spec.Engine != profile.Name || spec.Port != 9876 || !spec.IgnorePersistedPort {
			t.Errorf("%s: with a planned port got %+v, want 9876 and the persisted port ignored", profile.Name, spec)
		}
	}
}

// TestEngineProxyFallbackAvoidsItsOwnPorts checks the generic fallback picks a
// port for each engine from that engine's own range, not a hardcoded one.
func TestEngineProxyFallbackAvoidsItsOwnPorts(t *testing.T) {
	for _, profile := range engineProxyProfiles {
		b := &Broker{}
		port := b.setEngineProxyFallback(profile)
		if port == profile.FacadePort || port == profile.EnginePortBase {
			t.Errorf("%s: fallback %d collides with its own facade/backend ports", profile.Name, port)
		}
		if got := int(b.engineProxy(profile).startupPort.Load()); got != port {
			t.Errorf("%s: fallback %d was not recorded as the planned port (got %d)", profile.Name, port, got)
		}
		// A bind failure must not be answered with the port that just failed.
		if again := b.engineProxyFallbackPort(profile, port); again == port {
			t.Errorf("%s: fallback repeated the port that failed to bind (%d)", profile.Name, port)
		}
	}
}

// TestEveryEngineAdvertisesUnderItsOwnKey: the advertise loop and the settings
// path each named two engines, so MAX was never advertised and applying its
// settings withdrew Ollama's registration. Each engine must touch only its own
// discovery key.
func TestEveryEngineAdvertisesUnderItsOwnKey(t *testing.T) {
	for _, profile := range engineProxyProfiles {
		if profile.Name == ollamaProxyProfile.Name {
			continue // advertised by its own OLLAMA_HOST-aware loop
		}
		if !isEngineDiscoveryService(profile.DiscoveryService) {
			t.Errorf("%s: discovery key %q is not treated as an engine facade", profile.Name, profile.DiscoveryService)
		}
		b := &Broker{regCache: relay.NewRegistrationCache()}
		for _, other := range engineProxyProfiles {
			b.registerService(noderec.RegisterParams{Service: other.DiscoveryService, Port: other.FacadePort})
		}
		// No proxy is supervised, so this engine cannot be up.
		b.reconcileAdvertiseEngine(profile, nil)
		for _, reg := range b.regCache.Snapshot() {
			if reg.Service == profile.DiscoveryService {
				t.Errorf("%s: still advertised with no proxy", profile.Name)
			}
		}
		if got, want := len(b.regCache.Snapshot()), len(engineProxyProfiles)-1; got != want {
			t.Errorf("%s: reconcile left %d registrations, want %d (others untouched)", profile.Name, got, want)
		}
	}
}
