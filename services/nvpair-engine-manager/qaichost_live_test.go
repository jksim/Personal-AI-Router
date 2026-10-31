// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

//go:build live

package main

import (
	"os"
	"testing"

	"nvpair-shared/accel"
)

// TestLiveQAICHostReady runs the readiness check against this host's card and
// kernel command line. Docker is checked in the engine's start path, not here.
func TestLiveQAICHostReady(t *testing.T) {
	if os.Getenv("NVPAIR_LIVE_QAIC") != "1" {
		t.Skip("set NVPAIR_LIVE_QAIC=1 to check the real card")
	}
	cmdline, err := os.ReadFile("/proc/cmdline")
	if err != nil {
		t.Fatalf("read /proc/cmdline: %v", err)
	}
	socs, inspectErr := accel.InspectQAIC()
	ready, problems := checkQAICHost(socs, inspectErr, string(cmdline), nil)
	t.Logf("ready = %+v", ready)
	for _, p := range problems {
		t.Logf("problem (blocking=%v): %s %s", p.Blocking, p.Message, p.Fix)
	}
	if !ready.ok() {
		t.Fatal("host not ready")
	}
}
