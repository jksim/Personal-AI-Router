// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

//go:build live

package main

import (
	"context"
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

// TestLiveQAICCatalog checks that this host's catalogue offers the 4-SoC QPC.
func TestLiveQAICCatalog(t *testing.T) {
	if os.Getenv("NVPAIR_LIVE_QAIC") != "1" {
		t.Skip("set NVPAIR_LIVE_QAIC=1 to check the real card")
	}
	result, err := newCatalogService().Catalog(context.Background(), "qaic", "linux", "amd64", "")
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	for _, m := range result.Models {
		t.Logf("%s %v %d bytes", m.ID, m.Tags, m.Size)
	}
	if len(result.Models) == 0 || result.Models[0].ID != "Qwen/Qwen3-30B-A3B" {
		t.Fatal("the 4-SoC Qwen3 QPC is not offered on this host")
	}
}
