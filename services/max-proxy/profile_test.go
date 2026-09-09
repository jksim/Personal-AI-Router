// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"

	"nvpair-shared/noderec"
)

// TestMaxProfileIsValid runs the shipped profile through the same validation the
// binary does at startup. Without this the first sign of a malformed profile
// would be a proxy that starts, looks healthy, and misattributes every workload.
func TestMaxProfileIsValid(t *testing.T) {
	if err := maxProfile.Validate(); err != nil {
		t.Fatalf("the shipped MAX profile is invalid: %v", err)
	}
}

// TestMaxProfileIdentity pins the values other services agree with. Each of
// these is half of a contract held somewhere else in the tree, and a silent
// change here breaks the other half without failing here.
func TestMaxProfileIdentity(t *testing.T) {
	// The engine tag is what the scheduler ranks by and what a peer's model
	// inventory is keyed on. It must match schedulerEngines and the engine
	// manifest, not merely look reasonable.
	if maxProfile.Engine != "max" {
		t.Errorf("engine = %q, want max", maxProfile.Engine)
	}
	if maxProfile.ServiceKey != noderec.ServiceMax {
		t.Errorf("service key = %q, want the registered MAX key", maxProfile.ServiceKey)
	}
	// 8000 is MAX's own default and is taken as the proxy's facade so an
	// unmodified OpenAI client reaches the cluster; the broker moves the engine
	// itself to 8001.
	if maxProfile.DefaultPort != 8000 {
		t.Errorf("default port = %d, want 8000", maxProfile.DefaultPort)
	}
	// No deployed history, so nothing to migrate from.
	if maxProfile.LegacyPort != 0 {
		t.Errorf("legacy port = %d, want none", maxProfile.LegacyPort)
	}
	// Two engines sharing a port file would each restore the other's port and
	// fight over one socket.
	if maxProfile.PortFile == "lmstudio-proxy-port.json" {
		t.Error("MAX shares LM Studio's port file")
	}
	// nvpair-errors matches sticky ids literally, so a shared prefix would let
	// one engine clear another's errors.
	if maxProfile.ErrorIDPrefix == "lmstudio-proxy" || maxProfile.ErrorIDPrefix == "ollama-proxy" {
		t.Errorf("error id prefix %q collides with an existing engine", maxProfile.ErrorIDPrefix)
	}
}
