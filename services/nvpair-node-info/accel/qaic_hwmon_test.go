// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

package accel

import (
	"path/filepath"
	"testing"
)

func hwmonFiles(bdf, instance string, files map[string]string) map[string]string {
	out := map[string]string{}
	base := "bus/pci/devices/" + bdf + "/hwmon/" + instance + "/"
	for name, content := range files {
		out[base+name] = content
	}
	return out
}

// TestReadQAICSensorsAbsentIsInvalidNotZero is the case that will actually
// happen. The in-tree driver creates no hwmon nodes, so on a stock kernel every
// sensor is missing — and a missing sensor must never be published as a
// reading. Zero degrees and zero watts describe a plausible idle card, so a
// consumer cannot tell them apart from real values.
func TestReadQAICSensorsAbsentIsInvalidNotZero(t *testing.T) {
	root := sysfsTree(t, pciDevice("0000:05:00.0", "0x17cb", "0xa100"))

	sensors := readQAICSensors(root, "0000:05:00.0")

	for _, r := range []struct {
		name    string
		reading Reading
	}{
		{"temperature", sensors.TemperatureC},
		{"power", sensors.PowerW},
		{"power cap", sensors.PowerCapW},
	} {
		if r.reading.Valid {
			t.Fatalf("%s reported valid with no hwmon present: %+v", r.name, r.reading)
		}
	}
}

// TestReadQAICSensorsScalesUnits pins the hwmon conventions: millidegrees and
// microwatts. Getting a divisor wrong produces a number that looks like a
// plausible reading in the wrong unit.
func TestReadQAICSensorsScalesUnits(t *testing.T) {
	root := sysfsTree(t, mergeFiles(
		pciDevice("0000:05:00.0", "0x17cb", "0xa100"),
		hwmonFiles("0000:05:00.0", "hwmon3", map[string]string{
			"temp2_input":  "47000\n",
			"power1_input": "83500000\n",
			"power1_max":   "150000000\n",
		}),
	))

	sensors := readQAICSensors(root, "0000:05:00.0")

	if !sensors.TemperatureC.Valid || sensors.TemperatureC.Value != 47 {
		t.Fatalf("temperature = %+v, want 47 C", sensors.TemperatureC)
	}
	if !sensors.PowerW.Valid || sensors.PowerW.Value != 83.5 {
		t.Fatalf("power = %+v, want 83.5 W", sensors.PowerW)
	}
	if !sensors.PowerCapW.Valid || sensors.PowerCapW.Value != 150 {
		t.Fatalf("power cap = %+v, want 150 W", sensors.PowerCapW)
	}
}

// TestReadQAICSensorsPartialAndUnreadable covers a present hwmon whose files
// are incomplete or garbage. Each sensor stands on its own: one bad file must
// not invalidate the others, and must not become a zero.
func TestReadQAICSensorsPartialAndUnreadable(t *testing.T) {
	root := sysfsTree(t, mergeFiles(
		pciDevice("0000:05:00.0", "0x17cb", "0xa100"),
		hwmonFiles("0000:05:00.0", "hwmon0", map[string]string{
			"temp2_input": "41000\n",
			// power1_input absent entirely.
			"power1_max": "not-a-number\n",
		}),
	))

	sensors := readQAICSensors(root, "0000:05:00.0")

	if !sensors.TemperatureC.Valid || sensors.TemperatureC.Value != 41 {
		t.Fatalf("temperature = %+v, want 41 C alongside failing siblings", sensors.TemperatureC)
	}
	if sensors.PowerW.Valid {
		t.Fatalf("absent power file reported valid: %+v", sensors.PowerW)
	}
	if sensors.PowerCapW.Valid {
		t.Fatalf("unparseable power cap reported valid: %+v", sensors.PowerCapW)
	}
}

// TestReadQAICSensorsMissingDevice guards the path where the device vanished
// between enumeration and sampling.
func TestReadQAICSensorsMissingDevice(t *testing.T) {
	sensors := readQAICSensors(filepath.Join(t.TempDir(), "nonexistent"), "0000:05:00.0")
	if sensors.TemperatureC.Valid || sensors.PowerW.Valid || sensors.PowerCapW.Valid {
		t.Fatalf("sensors from a missing device reported valid: %+v", sensors)
	}
}
