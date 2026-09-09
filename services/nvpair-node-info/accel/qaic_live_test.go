// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

//go:build live

package accel

import (
	"context"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// Live tests against real Qualcomm hardware. Opt in with:
//
//	NVPAIR_LIVE_QAIC=1 go test -tags live -run TestLiveQAIC -v ./accel/
//
// Reading a device node needs permission to open /dev/accel/*, which is
// root-owned mode 0600 by default, so this may need sudo -E or a udev rule.
//
// Everything else in this package is verified against fixtures and against
// Qualcomm's headers. These tests are the only ones that can tell us the
// headers match the firmware, so until they have run green, the wire format is
// unverified no matter how many fixture tests pass.
//
// Never run `qaic-util -s`. It is --soc-reset and resets every device on the
// host, which would kill any running inference server.

func requireLiveQAIC(t *testing.T) []qaicSoC {
	t.Helper()
	if os.Getenv("NVPAIR_LIVE_QAIC") != "1" {
		t.Skip("set NVPAIR_LIVE_QAIC=1 to run against real hardware")
	}
	socs, err := enumerateQAIC(DefaultSysfsRoot)
	if err != nil {
		t.Fatalf("enumerateQAIC(%s): %v", DefaultSysfsRoot, err)
	}
	if len(socs) == 0 {
		t.Fatal("NVPAIR_LIVE_QAIC=1 but no Qualcomm accelerator is present")
	}
	return socs
}

// TestLiveQAICEnumerate reports what is on the bus and how it maps to device
// nodes. Run this first: if the SoC count is wrong, nothing downstream can be
// right, and on an Ultra a count of one rather than four means the PCIe ACS
// kernel parameter is missing — which fails silently.
func TestLiveQAICEnumerate(t *testing.T) {
	socs := requireLiveQAIC(t)

	nodes, err := mapAccelNodes(DefaultSysfsRoot)
	if err != nil {
		t.Fatalf("mapAccelNodes: %v", err)
	}
	for _, soc := range socs {
		t.Logf("QID %d  %s  %s (0x%04x)  node=%q", soc.QID, soc.BDF, soc.Model, soc.DeviceID, nodes[soc.BDF])
	}
	t.Logf("%d SoC(s), %d accel node(s)", len(socs), len(nodes))

	for _, soc := range socs {
		if _, ok := nodes[soc.BDF]; !ok {
			t.Errorf("%s has no /sys/class/accel node; the driver may not have bound it", soc.BDF)
		}
	}
}

// TestLiveQAICStatusRaw dumps the raw response before anything interprets it.
//
// This is the card-day probe. If the decode below disagrees with qaic-util,
// this hex is what tells us whether the request was wrong, the response was
// unexpected, or only our offsets are off.
func TestLiveQAICStatusRaw(t *testing.T) {
	socs := requireLiveQAIC(t)
	nodes, err := mapAccelNodes(DefaultSysfsRoot)
	if err != nil {
		t.Fatalf("mapAccelNodes: %v", err)
	}

	for _, soc := range socs {
		node, ok := nodes[soc.BDF]
		if !ok {
			continue
		}
		path := filepath.Join(DefaultDevRoot, node)
		response, err := qaicManage(path, encodeStatusRequest(), 1)
		if err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}
		t.Logf("%s: %d bytes, count=%d, nacked=%v", path, len(response.Payload), response.Count, response.NACKed)
		limit := len(response.Payload)
		if limit > nncStatusResponseLen {
			limit = nncStatusResponseLen
		}
		t.Logf("%s response:\n%s", path, hex.Dump(response.Payload[:limit]))
	}
}

// TestLiveQAICStatusDecode is the gate. Until it passes, the offsets are a
// reading of Qualcomm's headers and nothing more.
//
// Cross-check the values it logs against `qaic-util -q` before trusting
// anything built on them: board serial and NSP total are the two that matter,
// because card grouping and the load signal are derived from them.
func TestLiveQAICStatusDecode(t *testing.T) {
	socs := requireLiveQAIC(t)
	nodes, err := mapAccelNodes(DefaultSysfsRoot)
	if err != nil {
		t.Fatalf("mapAccelNodes: %v", err)
	}

	decoded := 0
	for _, soc := range socs {
		node, ok := nodes[soc.BDF]
		if !ok {
			continue
		}
		status, err := queryQAICStatus(filepath.Join(DefaultDevRoot, node))
		if err != nil {
			t.Errorf("%s (%s): %v", soc.BDF, node, err)
			continue
		}
		decoded++
		t.Logf("%s: serial=%q sku=%q dram=%d/%d KB nsp=%d/%d load=%.3f",
			soc.BDF, status.BoardSerial, status.SkuName,
			status.DramFreeKB, status.DramTotalKB,
			status.NspFree, status.NspTotal, status.Load().Fraction)

		if status.BoardSerial == "" {
			t.Errorf("%s: empty board serial; cards cannot be grouped without it", soc.BDF)
		}
		if status.NspTotal == 0 {
			t.Errorf("%s: zero NSPs reported; the load signal is derived from this", soc.BDF)
		}
		if status.DramTotalKB == 0 {
			t.Errorf("%s: zero total DRAM reported", soc.BDF)
		}
	}
	if decoded == 0 {
		// Distinguish the two very different causes, because they send you
		// to opposite ends of the machine:
		//   EACCES  — the node is there and we may not open it: join the
		//             group that owns /dev/accel/*.
		//   ENODEV  — the node is there and the device behind it is not.
		//             The driver bound but MHI never came up, which on this
		//             hardware has meant PCIe link errors. Check
		//             /sys/bus/pci/devices/<bdf>/aer_dev_correctable and
		//             the kernel log before suspecting anything in here.
		t.Fatal("no device could be decoded; see the per-device errors above — " +
			"permission denied means a group problem, no-such-device means the " +
			"hardware did not come up")
	}
}

// TestLiveQAICSourceReportsOneCardPerBoard is the whole module's headline
// claim, checked against real hardware: an Ultra's four SoCs must come back as
// one accelerator with the card's total memory, not four quarter-cards.
func TestLiveQAICSourceReportsOneCardPerBoard(t *testing.T) {
	socs := requireLiveQAIC(t)

	source := NewQAICSource()
	devices, err := source.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	for _, device := range devices {
		t.Logf("device: %s  %d MB  key=%s  load=%+v",
			device.Name, device.VramBytes/(1024*1024), device.StatsKey, device.Load)
	}

	if len(devices) == 0 {
		t.Fatal("hardware enumerated but no device reported")
	}
	if len(devices) > len(socs) {
		t.Fatalf("reported %d devices from %d SoCs", len(devices), len(socs))
	}
	if len(socs) > 1 && len(devices) == len(socs) {
		t.Errorf("%d SoCs reported as %d separate devices: grouping did not happen. "+
			"Expected on a multi-SoC card only if the device nodes are unreadable, "+
			"since the board serial is what groups them", len(socs), len(devices))
	}

	sampler, ok := source.(Sampler)
	if !ok {
		t.Fatal("the qaic source no longer implements Sampler")
	}
	samples, err := sampler.Sample(context.Background())
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}
	for _, sample := range samples {
		t.Logf("sample: key=%s load=%.3f used=%d MB",
			sample.StatsKey, sample.Load.Fraction, sample.MemoryUsedBytes/(1024*1024))
	}
	if len(samples) == 0 {
		t.Error("no samples produced; telemetry will not validate for this node")
	}
}

// TestLiveQAICSensors reports hwmon if it exists. It is expected not to on a
// stock kernel: the in-tree driver creates no hwmon nodes, so this documents
// what is available rather than asserting anything.
func TestLiveQAICSensors(t *testing.T) {
	socs := requireLiveQAIC(t)
	for _, soc := range socs {
		sensors := readQAICSensors(DefaultSysfsRoot, soc.BDF)
		t.Logf("%s: temp=%+v power=%+v cap=%+v", soc.BDF, sensors.TemperatureC, sensors.PowerW, sensors.PowerCapW)
	}
}
