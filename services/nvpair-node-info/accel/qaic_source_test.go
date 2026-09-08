// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

package accel

import (
	"context"
	"errors"
	"testing"
)

func ultraFixture(t *testing.T) string {
	t.Helper()
	root := sysfsTree(t, mergeFiles(
		pciDevice("0000:05:00.0", "0x17cb", "0xa100"),
		pciDevice("0000:09:00.0", "0x17cb", "0xa100"),
		pciDevice("0000:0d:00.0", "0x17cb", "0xa100"),
		pciDevice("0000:11:00.0", "0x17cb", "0xa100"),
		// The WiFi adapter this host actually has, which must be ignored.
		pciDevice("0000:34:00.0", "0x17cb", "0x1103"),
	))
	accelNodeTree(t, root, map[string]string{
		"accel0": "0000:05:00.0",
		"accel1": "0000:09:00.0",
		"accel2": "0000:0d:00.0",
		"accel3": "0000:11:00.0",
	})
	return root
}

func ultraStatus(devicePath string) (qaicStatus, error) {
	return qaicStatus{
		BoardSerial: "ULTRA-0001",
		DramTotalMB: 32768,
		DramFreeMB:  32768,
		NspTotal:    16,
		NspFree:     16,
		SkuName:     "PCIe Ultra",
	}, nil
}

// TestQAICSourceReportsOneCard is the end-to-end shape: four PCI functions in,
// one accelerator out, with the card's total memory rather than one SoC's.
func TestQAICSourceReportsOneCard(t *testing.T) {
	source := &qaicSource{sysfsRoot: ultraFixture(t), devRoot: "/dev/accel", query: ultraStatus}

	devices, err := source.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if len(devices) != 1 {
		t.Fatalf("got %d devices, want one card: %+v", len(devices), devices)
	}
	device := devices[0]
	if device.Name != "Qualcomm Cloud AI PCIe Ultra" {
		t.Fatalf("name = %q", device.Name)
	}
	if want := uint64(131072) * 1024 * 1024; device.VramBytes != want {
		t.Fatalf("memory = %d bytes, want the summed %d", device.VramBytes, want)
	}
	if device.StatsKey != "qaic:serial:ULTRA-0001" {
		t.Fatalf("stats key = %q, want the board serial", device.StatsKey)
	}
	if !device.Load.Valid || device.Load.Fraction != 0 {
		t.Fatalf("load = %+v, want a valid idle reading", device.Load)
	}
}

// TestQAICSourceAbsentHardware is the case on every host without a card,
// including the one this is being developed on.
func TestQAICSourceAbsentHardware(t *testing.T) {
	root := sysfsTree(t, pciDevice("0000:34:00.0", "0x17cb", "0x1103"))
	source := &qaicSource{sysfsRoot: root, devRoot: "/dev/accel", query: ultraStatus}

	devices, err := source.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect() error = %v, want nil for absent hardware", err)
	}
	if len(devices) != 0 {
		t.Fatalf("got %d devices from a host with only a WiFi adapter", len(devices))
	}
}

// TestQAICSourceUnreadableDevicesStillReportHardware is the permissions case,
// and it is the likely one: /dev/accel/* is root-owned mode 0600 by default and
// node-info runs unprivileged. A card the operator can see in lspci must not
// disappear from the UI because we could not open its device node.
func TestQAICSourceUnreadableDevicesStillReportHardware(t *testing.T) {
	denied := func(string) (qaicStatus, error) { return qaicStatus{}, errors.New("permission denied") }
	source := &qaicSource{sysfsRoot: ultraFixture(t), devRoot: "/dev/accel", query: denied}

	devices, err := source.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect() error = %v, want the hardware reported anyway", err)
	}
	// Without a serial the SoCs cannot be grouped, so each is reported
	// separately rather than merged on an assumption.
	if len(devices) != 4 {
		t.Fatalf("got %d devices, want the four SoCs reported ungrouped", len(devices))
	}
	for _, device := range devices {
		if device.Load.Valid {
			t.Fatalf("device %q reported a load with no readable status: %+v", device.Name, device.Load)
		}
		if device.VramBytes != 0 {
			t.Fatalf("device %q invented %d bytes of memory", device.Name, device.VramBytes)
		}
		if device.StatsKey == "" {
			t.Fatalf("device %q has no stats key, so it can never gain dynamic readings", device.Name)
		}
	}
}

// TestQAICSourceLoadReflectsBusyNSPs checks the load signal survives the whole
// path, not just the decoder.
func TestQAICSourceLoadReflectsBusyNSPs(t *testing.T) {
	busy := func(string) (qaicStatus, error) {
		return qaicStatus{
			BoardSerial: "ULTRA-0001", DramTotalMB: 32768, DramFreeMB: 16384,
			NspTotal: 16, NspFree: 8, SkuName: "PCIe Ultra",
		}, nil
	}
	source := &qaicSource{sysfsRoot: ultraFixture(t), devRoot: "/dev/accel", query: busy}

	devices, err := source.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if len(devices) != 1 {
		t.Fatalf("got %d devices, want 1", len(devices))
	}
	if load := devices[0].Load; !load.Valid || load.Fraction != 0.5 {
		t.Fatalf("load = %+v, want half the NSPs busy across the card", load)
	}
}
