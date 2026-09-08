// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

package accel

import (
	"os"
	"path/filepath"
	"strings"
)

// Mapping PCI functions to device nodes, and grouping SoCs back into the
// physical cards they belong to.

// qaicSoCStatus pairs an enumerated SoC with what the device reported, if it
// could be asked. HasStatus is false when the device node could not be opened
// or the query failed — a common case, since the nodes are root-owned by
// default.
type qaicSoCStatus struct {
	SoC       qaicSoC
	Status    qaicStatus
	HasStatus bool
}

// qaicCard is one physical accelerator: the SoCs sharing a board serial, with
// their resources summed.
type qaicCard struct {
	// Serial is the board serial the SoCs agreed on, empty when unknown.
	Serial string

	// Model is the PCI model name, e.g. "AIC100".
	Model string

	// SkuName is the marketing SKU the device reports, e.g. "PCIe Ultra".
	SkuName string

	// SoCs are the members, in PCI address order.
	SoCs []qaicSoC

	// Grouped reports that membership was established from a board serial
	// rather than assumed. A card with one SoC and no serial is not grouped.
	Grouped bool

	// Summed resources across the members. Zero when no member answered.
	DramTotalMB uint32
	DramFreeMB  uint32
	NspTotal    int
	NspFree     int
}

// Load is occupancy across the whole card.
//
// A card nothing answered for reports invalid rather than idle: the scheduler
// treats an idle node as a preferred target, so guessing here sends real work
// to hardware whose state is unknown.
func (c qaicCard) Load() Load {
	if c.NspTotal == 0 {
		return Load{}
	}
	free := c.NspFree
	if free > c.NspTotal {
		free = c.NspTotal
	}
	return Load{Fraction: float64(c.NspTotal-free) / float64(c.NspTotal), Valid: true}
}

// mapAccelNodes returns PCI address -> accel device node name, read from
// /sys/class/accel/accel*/device.
//
// The mapping cannot be derived: accel minor numbers are assigned in
// driver-probe order, which need not match PCI address order. A host where the
// driver never loaded has no such directory, which is not an error.
func mapAccelNodes(sysfsRoot string) (map[string]string, error) {
	classDir := filepath.Join(sysfsRoot, "class", "accel")
	entries, err := os.ReadDir(classDir)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}

	mapping := make(map[string]string, len(entries))
	for _, entry := range entries {
		node := entry.Name()
		if !strings.HasPrefix(node, "accel") {
			continue
		}
		// Read the link rather than resolving it: the target is a relative
		// path into the device tree whose last element is the PCI address,
		// and resolving would require the whole tree to exist.
		target, err := os.Readlink(filepath.Join(classDir, node, "device"))
		if err != nil {
			continue
		}
		mapping[filepath.Base(target)] = node
	}
	return mapping, nil
}

// groupQAICCards folds SoCs into the cards they physically belong to.
//
// Board serial is the only grouping key. Bus topology is deliberately not used
// as a fallback: SoCs of one card do share an upstream switch, but so do two
// separate cards in adjacent slots, and merging those would report one phantom
// card with double the capacity — worse than reporting the SoCs separately.
//
// So a SoC whose status could not be read stands alone, and says so through
// Grouped. That understates a card whose devices could not be opened, but it
// never invents hardware, and the understatement points at a fixable cause: the
// device nodes are root-owned by default.
//
// Cards come back ordered by their first member's PCI address, which is stable
// across boots.
func groupQAICCards(entries []qaicSoCStatus) []qaicCard {
	var cards []qaicCard
	bySerial := make(map[string]int, len(entries))

	for _, entry := range entries {
		serial := ""
		if entry.HasStatus {
			serial = entry.Status.BoardSerial
		}

		if serial != "" {
			if index, seen := bySerial[serial]; seen {
				cards[index] = addSoC(cards[index], entry)
				continue
			}
			bySerial[serial] = len(cards)
		}
		cards = append(cards, addSoC(qaicCard{
			Serial:  serial,
			Model:   entry.SoC.Model,
			Grouped: serial != "",
		}, entry))
	}
	return cards
}

// addSoC appends one SoC to a card and accumulates its resources.
func addSoC(card qaicCard, entry qaicSoCStatus) qaicCard {
	card.SoCs = append(card.SoCs, entry.SoC)
	if !entry.HasStatus {
		return card
	}
	if card.SkuName == "" {
		card.SkuName = entry.Status.SkuName
	}
	card.DramTotalMB += entry.Status.DramTotalMB
	card.DramFreeMB += entry.Status.DramFreeMB
	card.NspTotal += int(entry.Status.NspTotal)
	card.NspFree += int(entry.Status.NspFree)
	return card
}
