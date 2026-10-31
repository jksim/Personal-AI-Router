// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

package accel

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// DefaultSysfsRoot is the mount point the enumerator reads. Tests inject a
// synthetic tree instead, which is the only reason this is a parameter at all.
const DefaultSysfsRoot = "/sys"

// qaicVendorID is Qualcomm's PCI vendor id. It spans their entire product
// line, so it is never sufficient on its own: this host carries a Qualcomm
// WiFi adapter (0x17cb:0x1103) that would otherwise be reported as an
// accelerator. Every device id below must also match.
const qaicVendorID = 0x17cb

// qaicModels are the devices the qaic driver binds, from its own PCI id table
// (qaic_drv.c) and the ids in qaic.h. Sourced from the driver rather than from
// documentation so the two cannot drift.
//
// Note the in-tree driver shipped with Linux 6.8 advertises only AIC080 and
// AIC100; AIC200 and its virtual function bind on newer kernels. Listing all
// four here is harmless — a device that cannot bind will not appear in sysfs.
var qaicModels = map[uint16]string{
	0xa080: "AIC080",
	0xa100: "AIC100",
	0xa110: "AIC200",
	0xa111: "AIC200VF",
}

// qaicSoC is one Qualcomm accelerator SoC as it appears on the PCI bus.
//
// A card is not always one SoC: a Cloud AI 100 Ultra presents four, each its
// own PCI function with its own memory and compute resources. Grouping those
// back into a card is a separate concern from finding them.
type qaicSoC struct {
	// BDF is the PCI address, e.g. "0000:05:00.0".
	BDF string

	// DeviceID is the PCI device id, and Model its name from the driver's
	// table.
	DeviceID uint16
	Model    string

	// QID is this SoC's index in ascending BDF order, reproducing the
	// numbering Qualcomm's own runtime assigns. Anything reporting a QID
	// must agree with qaic-util about which SoC is which.
	QID int
}

// enumerateQAIC lists the Qualcomm accelerator SoCs visible under sysfsRoot,
// ordered by PCI address with QIDs assigned from that order.
//
// A host with no such hardware — or without the PCI bus path at all — yields no
// SoCs and no error: absent hardware is the ordinary case, not a fault. Entries
// that cannot be read are skipped rather than failing the scan, because devices
// bind and unbind while the directory is being walked.
func enumerateQAIC(sysfsRoot string) ([]qaicSoC, error) {
	devicesDir := filepath.Join(sysfsRoot, "bus", "pci", "devices")
	entries, err := os.ReadDir(devicesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var socs []qaicSoC
	for _, entry := range entries {
		bdf := entry.Name()
		vendor, ok := readPCIID(filepath.Join(devicesDir, bdf, "vendor"))
		if !ok || vendor != qaicVendorID {
			continue
		}
		device, ok := readPCIID(filepath.Join(devicesDir, bdf, "device"))
		if !ok {
			continue
		}
		model, ok := qaicModels[device]
		if !ok {
			continue
		}
		socs = append(socs, qaicSoC{BDF: bdf, DeviceID: device, Model: model})
	}

	sort.Slice(socs, func(i, j int) bool { return socs[i].BDF < socs[j].BDF })
	for i := range socs {
		socs[i].QID = i
	}
	return socs, nil
}

// readPCIID reads a sysfs id file such as "0x17cb". It reports failure rather
// than an error because every caller treats an unreadable entry the same way:
// skip it and keep scanning.
func readPCIID(path string) (uint16, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	text := strings.TrimSpace(string(raw))
	value, err := strconv.ParseUint(strings.TrimPrefix(text, "0x"), 16, 16)
	if err != nil {
		return 0, false
	}
	return uint16(value), true
}
