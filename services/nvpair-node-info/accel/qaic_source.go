// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

package accel

import (
	"context"
	"fmt"
	"path/filepath"
)

// SourceQAIC is the source name used in logs. It names the interface being
// used rather than the vendor, because that is what an operator checks.
const SourceQAIC = "qaic"

// DefaultDevRoot is where the driver's device nodes appear.
const DefaultDevRoot = "/dev/accel"

// qaicSource detects Qualcomm accelerators.
//
// Enumeration needs nothing but sysfs. Reading a device's state needs its
// device node opened, which is root-owned and mode 0600 by default, so an
// unprivileged process routinely gets as far as "a card is here" and no
// further. That is treated as partial success: the hardware is reported
// without a load signal, rather than the whole source failing.
type qaicSource struct {
	sysfsRoot string
	devRoot   string

	// query reads one device's status. Injected so the source is testable
	// without hardware.
	query func(devicePath string) (qaicStatus, error)
}

// NewQAICSource builds the source against the real system paths.
func NewQAICSource() Source {
	return &qaicSource{
		sysfsRoot: DefaultSysfsRoot,
		devRoot:   DefaultDevRoot,
		query:     queryQAICStatus,
	}
}

func (s *qaicSource) Name() string { return SourceQAIC }

// Detect enumerates the accelerators and describes them as cards.
//
// An enumeration failure is a real error. Everything after it degrades
// instead: a device that cannot be queried still appears, without memory or
// load figures, because a card the operator can see in lspci should not vanish
// from the UI because of a permissions problem.
func (s *qaicSource) Detect(context.Context) ([]Device, error) {
	cards, err := s.cards()
	if err != nil {
		return nil, err
	}
	devices := make([]Device, 0, len(cards))
	for _, card := range cards {
		devices = append(devices, Device{
			Name:      qaicDeviceName(card),
			VramBytes: card.DramTotalKB * 1024,
			StatsKey:  qaicStatsKey(card),
			Vendor:    VendorQualcomm,
			Kind:      KindAccelerator,
			Load:      card.Load(),
		})
	}
	return devices, nil
}

// Sample re-reads the cards' current state.
//
// It re-enumerates rather than caching what Detect found. Enumeration is a
// handful of small file reads, and a card that was hot-plugged, or whose device
// node became readable after a udev rule was added, then appears without
// needing a restart — which matters because detection otherwise runs only once
// at boot.
func (s *qaicSource) Sample(context.Context) ([]DeviceSample, error) {
	cards, err := s.cards()
	if err != nil {
		return nil, err
	}
	samples := make([]DeviceSample, 0, len(cards))
	for _, card := range cards {
		load := card.Load()
		if !load.Valid {
			// Nothing was readable for this card. Publishing a zero
			// would be indistinguishable from a genuinely idle card.
			continue
		}
		used := (card.DramTotalKB - card.DramFreeKB) * 1024
		samples = append(samples, DeviceSample{
			StatsKey:        qaicStatsKey(card),
			Load:            load,
			MemoryUsedBytes: used,
		})
	}
	return samples, nil
}

// cards enumerates the SoCs, asks each for its status where possible, and
// groups them into physical cards.
func (s *qaicSource) cards() ([]qaicCard, error) {
	socs, err := enumerateQAIC(s.sysfsRoot)
	if err != nil {
		return nil, fmt.Errorf("enumerate qaic devices: %w", err)
	}
	if len(socs) == 0 {
		return nil, nil
	}

	nodes, err := mapAccelNodes(s.sysfsRoot)
	if err != nil {
		// Enumeration worked, so the hardware is real; without the node
		// map we simply cannot query it.
		nodes = map[string]string{}
	}

	entries := make([]qaicSoCStatus, 0, len(socs))
	for _, soc := range socs {
		entry := qaicSoCStatus{SoC: soc}
		if node, ok := nodes[soc.BDF]; ok {
			if status, err := s.query(filepath.Join(s.devRoot, node)); err == nil {
				entry.Status = status
				entry.HasStatus = true
			}
		}
		entries = append(entries, entry)
	}
	return groupQAICCards(entries), nil
}

// qaicDeviceName is what an operator sees. The SKU is the useful part — it
// distinguishes an Ultra from a standard card — so it is used when the device
// reported one, and the PCI model name is the fallback.
func qaicDeviceName(card qaicCard) string {
	if card.SkuName != "" {
		return fmt.Sprintf("Qualcomm Cloud AI %s", card.SkuName)
	}
	return fmt.Sprintf("Qualcomm Cloud AI %s", card.Model)
}

// qaicStatsKey identifies a card across samples and restarts. The board serial
// is the stable choice; without one, the first SoC's PCI address is stable for
// as long as the card stays in its slot.
func qaicStatsKey(card qaicCard) string {
	if card.Serial != "" {
		return "qaic:serial:" + card.Serial
	}
	if len(card.SoCs) > 0 {
		return "qaic:pci:" + card.SoCs[0].BDF
	}
	return ""
}

// queryQAICStatus runs one status transaction against a device node.
func queryQAICStatus(devicePath string) (qaicStatus, error) {
	response, err := qaicManage(devicePath, encodeStatusRequest(), 1)
	if err != nil {
		return qaicStatus{}, err
	}
	if response.NACKed {
		return qaicStatus{}, fmt.Errorf("qaic status: device rejected the request")
	}
	return decodeStatusResponse(response.Payload)
}
