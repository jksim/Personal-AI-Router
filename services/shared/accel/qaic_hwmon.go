// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

package accel

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Thermal and power sensors, when the driver exposes them.
//
// These come from hwmon, which only the out-of-tree Qualcomm KMD creates: the
// in-tree driver has no hwmon support at all, and telemetry was explicitly
// dropped from its upstreaming. So on a stock kernel this reads nothing, and
// that is the expected case rather than a fault. Everything the scheduler needs
// comes from the status query instead; these are for display.
//
// PAIR never installs the out-of-tree module. If an operator has installed it,
// the sensors appear and are read.

// Reading is one sensor value that may not be available.
//
// Validity is explicit because Qualcomm's own reader returns zero on a failed
// read while still reporting success. Publishing that as a value shows a card
// sitting at 0 °C and drawing 0 W, which reads as a working sensor on a cold
// idle card rather than as no sensor at all.
type Reading struct {
	Value float64
	Valid bool
}

// qaicSensors are the hwmon values Qualcomm's runtime reads.
type qaicSensors struct {
	// TemperatureC is the SoC temperature. Qualcomm reads temp2_input, not
	// temp1_input.
	TemperatureC Reading

	// PowerW is board power draw, PowerCapW the configured TDP cap.
	PowerW    Reading
	PowerCapW Reading
}

// readQAICSensors reads the hwmon instance under a PCI device, if there is one.
//
// It never returns an error: an absent hwmon directory is the normal case on a
// stock kernel, and an unreadable file is reported as an invalid reading rather
// than failing the whole sample.
func readQAICSensors(sysfsRoot, bdf string) qaicSensors {
	dir, ok := findHwmonDir(filepath.Join(sysfsRoot, "bus", "pci", "devices", bdf, "hwmon"))
	if !ok {
		return qaicSensors{}
	}
	return qaicSensors{
		// hwmon reports millidegrees and microwatts.
		TemperatureC: readSensor(filepath.Join(dir, "temp2_input"), 1000),
		PowerW:       readSensor(filepath.Join(dir, "power1_input"), 1_000_000),
		PowerCapW:    readSensor(filepath.Join(dir, "power1_max"), 1_000_000),
	}
}

// findHwmonDir locates the hwmonN subdirectory a device registers.
func findHwmonDir(hwmonRoot string) (string, bool) {
	entries, err := os.ReadDir(hwmonRoot)
	if err != nil {
		return "", false
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "hwmon") {
			return filepath.Join(hwmonRoot, entry.Name()), true
		}
	}
	return "", false
}

// readSensor reads one hwmon file and scales it into a base unit. A missing or
// unparseable file yields an invalid reading, never a zero value.
func readSensor(path string, divisor float64) Reading {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Reading{}
	}
	value, err := strconv.ParseFloat(strings.TrimSpace(string(raw)), 64)
	if err != nil {
		return Reading{}
	}
	return Reading{Value: value / divisor, Valid: true}
}
