// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

package accel

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// accelNodeTree adds /sys/class/accel/accelN -> ../../devices/.../<BDF> links
// to a sysfs fixture, mirroring how the driver exposes its device nodes.
func accelNodeTree(t *testing.T, root string, nodes map[string]string) {
	t.Helper()
	for node, bdf := range nodes {
		dir := filepath.Join(root, "class", "accel", node)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
		target := filepath.Join("../../devices/pci0000:00", bdf)
		if err := os.Symlink(target, filepath.Join(dir, "device")); err != nil {
			t.Fatalf("symlink %s: %v", node, err)
		}
	}
}

// TestMapAccelNodes resolves PCI addresses to the device node that must be
// opened to talk to them. The numbering is not derivable: accel minors are
// handed out in driver-probe order, which need not match PCI address order.
func TestMapAccelNodes(t *testing.T) {
	root := sysfsTree(t, nil)
	accelNodeTree(t, root, map[string]string{
		"accel1": "0000:05:00.0",
		"accel2": "0000:09:00.0",
		// Deliberately inverted against BDF order.
		"accel0": "0000:11:00.0",
	})

	mapping, err := mapAccelNodes(root)
	if err != nil {
		t.Fatalf("mapAccelNodes() error = %v", err)
	}
	want := map[string]string{
		"0000:05:00.0": "accel1",
		"0000:09:00.0": "accel2",
		"0000:11:00.0": "accel0",
	}
	if !reflect.DeepEqual(mapping, want) {
		t.Fatalf("mapping = %v, want %v", mapping, want)
	}
}

// TestMapAccelNodesMissingClassDir matches the Source contract: a host where
// the driver never loaded has no such directory, and that is not a failure.
func TestMapAccelNodesMissingClassDir(t *testing.T) {
	mapping, err := mapAccelNodes(filepath.Join(t.TempDir(), "nonexistent"))
	if err != nil {
		t.Fatalf("mapAccelNodes() error = %v, want nil", err)
	}
	if len(mapping) != 0 {
		t.Fatalf("mapping = %v, want empty", mapping)
	}
}

func socWithStatus(bdf, serial string, dramTotal, dramFree uint32, nspTotal, nspFree uint8) qaicSoCStatus {
	return qaicSoCStatus{
		SoC: qaicSoC{BDF: bdf, DeviceID: 0xa100, Model: "AIC100"},
		Status: qaicStatus{
			BoardSerial: serial,
			DramTotalMB: dramTotal,
			DramFreeMB:  dramFree,
			NspTotal:    nspTotal,
			NspFree:     nspFree,
			SkuName:     "PCIe Ultra",
		},
		HasStatus: true,
	}
}

// TestGroupQAICCardsUltraIsOneCard is the headline requirement. A Cloud AI 100
// Ultra presents four PCI functions, each reporting its own ~32 GB and 16 NSPs.
// Reported as-is the UI shows four accelerators and the operator has no idea
// how much card they actually have; this test fails if that regresses.
func TestGroupQAICCardsUltraIsOneCard(t *testing.T) {
	const serial = "ULTRA-0001"
	cards := groupQAICCards([]qaicSoCStatus{
		socWithStatus("0000:05:00.0", serial, 32768, 32768, 16, 16),
		socWithStatus("0000:09:00.0", serial, 32768, 24576, 16, 12),
		socWithStatus("0000:0d:00.0", serial, 32768, 32768, 16, 16),
		socWithStatus("0000:11:00.0", serial, 32768, 32768, 16, 16),
	})

	if len(cards) != 1 {
		t.Fatalf("got %d cards, want one Ultra (four SoCs must not read as four accelerators)", len(cards))
	}
	card := cards[0]
	if len(card.SoCs) != 4 {
		t.Fatalf("card holds %d SoCs, want 4", len(card.SoCs))
	}
	if card.DramTotalMB != 131072 {
		t.Fatalf("card DRAM = %d MB, want the sum 131072", card.DramTotalMB)
	}
	if card.NspTotal != 64 || card.NspFree != 60 {
		t.Fatalf("card NSP = %d/%d, want summed 60/64", card.NspFree, card.NspTotal)
	}
	// One of sixteen groups of four busy: 4/64.
	load := card.Load()
	if !load.Valid || load.Fraction != 4.0/64.0 {
		t.Fatalf("card load = %+v, want 4/64 across the whole card", load)
	}
}

// TestGroupQAICCardsSeparatesDistinctSerials keeps two physically separate
// cards separate. This is the failure mode that ruled out grouping by PCIe
// topology: two single-SoC cards behind one switch share an upstream ancestor
// and would have merged into a phantom double-capacity card.
func TestGroupQAICCardsSeparatesDistinctSerials(t *testing.T) {
	cards := groupQAICCards([]qaicSoCStatus{
		socWithStatus("0000:05:00.0", "CARD-A", 32768, 32768, 16, 16),
		socWithStatus("0000:09:00.0", "CARD-B", 32768, 32768, 16, 16),
	})

	if len(cards) != 2 {
		t.Fatalf("got %d cards, want 2 distinct serials kept apart", len(cards))
	}
	if cards[0].Serial != "CARD-A" || cards[1].Serial != "CARD-B" {
		t.Fatalf("cards = %+v, want ordered by first SoC's PCI address", cards)
	}
}

// TestGroupQAICCardsWithoutStatusStaysUngrouped is the honest-degradation
// path. Without a status query there is no serial, and there is no second
// signal worth trusting: guessing from bus topology risks merging unrelated
// cards. Each SoC is reported on its own instead, which understates the card
// but never invents one.
func TestGroupQAICCardsWithoutStatusStaysUngrouped(t *testing.T) {
	entries := []qaicSoCStatus{
		{SoC: qaicSoC{BDF: "0000:05:00.0", Model: "AIC100"}},
		{SoC: qaicSoC{BDF: "0000:09:00.0", Model: "AIC100"}},
	}
	cards := groupQAICCards(entries)

	if len(cards) != 2 {
		t.Fatalf("got %d cards, want each unidentified SoC on its own", len(cards))
	}
	for _, card := range cards {
		if card.Grouped {
			t.Fatalf("card %+v claims to be grouped without a serial", card)
		}
		if load := card.Load(); load.Valid {
			t.Fatalf("card without status reported a valid load %+v", load)
		}
	}
}

// TestGroupQAICCardsMixedStatus covers a partially readable card: one SoC
// answered and its siblings did not. The ones that answered group together;
// the silent ones stand alone rather than being folded in on an assumption.
func TestGroupQAICCardsMixedStatus(t *testing.T) {
	cards := groupQAICCards([]qaicSoCStatus{
		socWithStatus("0000:05:00.0", "ULTRA-0002", 32768, 32768, 16, 16),
		{SoC: qaicSoC{BDF: "0000:09:00.0", Model: "AIC100"}},
		socWithStatus("0000:0d:00.0", "ULTRA-0002", 32768, 32768, 16, 16),
	})

	if len(cards) != 2 {
		t.Fatalf("got %d cards, want the two identified SoCs grouped and the silent one alone", len(cards))
	}
	if !cards[0].Grouped || len(cards[0].SoCs) != 2 {
		t.Fatalf("first card = %+v, want the two SoCs sharing a serial", cards[0])
	}
	if cards[1].Grouped || len(cards[1].SoCs) != 1 {
		t.Fatalf("second card = %+v, want the unidentified SoC alone", cards[1])
	}
}
