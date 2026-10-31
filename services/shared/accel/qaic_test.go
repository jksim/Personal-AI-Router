// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

package accel

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// sysfsTree writes a synthetic /sys into a temp dir. Fixtures stay visible as
// Go literals, which is this repository's convention, while the code under test
// still walks a real directory rather than a mocked filesystem.
func sysfsTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for path, content := range files {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", full, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
	return root
}

// pciDevice is the minimal sysfs shape the enumerator reads for one function.
func pciDevice(bdf, vendor, device string) map[string]string {
	base := "bus/pci/devices/" + bdf + "/"
	return map[string]string{
		base + "vendor": vendor + "\n",
		base + "device": device + "\n",
	}
}

func mergeFiles(sets ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, set := range sets {
		for k, v := range set {
			out[k] = v
		}
	}
	return out
}

func bdfsOf(socs []qaicSoC) []string {
	if len(socs) == 0 {
		// Normalise so an empty result compares equal to a nil want.
		return nil
	}
	out := make([]string, 0, len(socs))
	for _, soc := range socs {
		out = append(out, soc.BDF)
	}
	return out
}

// TestEnumerateQAICSelectsOnlyAccelerators is the core filter. Vendor alone is
// not enough: 0x17cb is Qualcomm's vendor id across their whole product line,
// and the development host for this work carries a Qualcomm WiFi adapter at
// 0000:34:00.0 with device id 0x1103. Matching on vendor would report that
// adapter as an AI 100.
func TestEnumerateQAICSelectsOnlyAccelerators(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{
			name:  "no pci devices at all",
			files: map[string]string{},
			want:  nil,
		},
		{
			name:  "qualcomm wifi is not an accelerator",
			files: pciDevice("0000:34:00.0", "0x17cb", "0x1103"),
			want:  nil,
		},
		{
			name:  "nvidia gpu is not ours",
			files: pciDevice("0000:ac:00.0", "0x10de", "0x2232"),
			want:  nil,
		},
		{
			name:  "single AIC100",
			files: pciDevice("0000:05:00.0", "0x17cb", "0xa100"),
			want:  []string{"0000:05:00.0"},
		},
		{
			name: "accelerator alongside wifi and a gpu",
			files: mergeFiles(
				pciDevice("0000:34:00.0", "0x17cb", "0x1103"),
				pciDevice("0000:ac:00.0", "0x10de", "0x2232"),
				pciDevice("0000:05:00.0", "0x17cb", "0xa100"),
			),
			want: []string{"0000:05:00.0"},
		},
		{
			name: "every id the driver binds",
			files: mergeFiles(
				pciDevice("0000:01:00.0", "0x17cb", "0xa080"),
				pciDevice("0000:02:00.0", "0x17cb", "0xa100"),
				pciDevice("0000:03:00.0", "0x17cb", "0xa110"),
				pciDevice("0000:04:00.0", "0x17cb", "0xa111"),
			),
			want: []string{"0000:01:00.0", "0000:02:00.0", "0000:03:00.0", "0000:04:00.0"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			socs, err := enumerateQAIC(sysfsTree(t, c.files))
			if err != nil {
				t.Fatalf("enumerateQAIC() error = %v", err)
			}
			if got := bdfsOf(socs); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("BDFs = %v, want %v", got, c.want)
			}
		})
	}
}

// TestEnumerateQAICAssignsQIDByBDFOrder reproduces Qualcomm's own numbering.
// Their runtime sorts matching devices by PCI address and uses the index as the
// QID, so anything that reports a QID has to sort the same way or it will
// disagree with qaic-util about which SoC is which.
//
// The fixture is supplied out of order deliberately: directory iteration order
// is not a sort, and a test fed pre-sorted input would pass without the sort.
func TestEnumerateQAICAssignsQIDByBDFOrder(t *testing.T) {
	root := sysfsTree(t, mergeFiles(
		pciDevice("0000:0d:00.0", "0x17cb", "0xa100"),
		pciDevice("0000:05:00.0", "0x17cb", "0xa100"),
		pciDevice("0000:11:00.0", "0x17cb", "0xa100"),
		pciDevice("0000:09:00.0", "0x17cb", "0xa100"),
	))

	socs, err := enumerateQAIC(root)
	if err != nil {
		t.Fatalf("enumerateQAIC() error = %v", err)
	}

	wantBDF := []string{"0000:05:00.0", "0000:09:00.0", "0000:0d:00.0", "0000:11:00.0"}
	if got := bdfsOf(socs); !reflect.DeepEqual(got, wantBDF) {
		t.Fatalf("BDFs = %v, want ascending %v", got, wantBDF)
	}
	for i, soc := range socs {
		if soc.QID != i {
			t.Fatalf("%s has QID %d, want %d (index in BDF order)", soc.BDF, soc.QID, i)
		}
	}
}

// TestEnumerateQAICReportsModel keeps the device id resolved to the name the
// operator will recognise, rather than leaving a bare hex id in the UI.
func TestEnumerateQAICReportsModel(t *testing.T) {
	root := sysfsTree(t, pciDevice("0000:05:00.0", "0x17cb", "0xa100"))
	socs, err := enumerateQAIC(root)
	if err != nil {
		t.Fatalf("enumerateQAIC() error = %v", err)
	}
	if len(socs) != 1 {
		t.Fatalf("got %d SoCs, want 1", len(socs))
	}
	if socs[0].Model != "AIC100" || socs[0].DeviceID != 0xa100 {
		t.Fatalf("soc = %+v, want AIC100 / 0xa100", socs[0])
	}
}

// TestEnumerateQAICToleratesUnreadableEntries keeps one malformed sysfs entry
// from hiding working hardware. Entries appear and disappear as devices bind,
// so a partially readable tree is a normal race, not a fault.
func TestEnumerateQAICToleratesUnreadableEntries(t *testing.T) {
	root := sysfsTree(t, mergeFiles(
		// No device file at all.
		map[string]string{"bus/pci/devices/0000:06:00.0/vendor": "0x17cb\n"},
		// Unparseable ids.
		map[string]string{
			"bus/pci/devices/0000:07:00.0/vendor": "not-hex\n",
			"bus/pci/devices/0000:07:00.0/device": "0xa100\n",
		},
		pciDevice("0000:05:00.0", "0x17cb", "0xa100"),
	))

	socs, err := enumerateQAIC(root)
	if err != nil {
		t.Fatalf("enumerateQAIC() error = %v", err)
	}
	if got := bdfsOf(socs); !reflect.DeepEqual(got, []string{"0000:05:00.0"}) {
		t.Fatalf("BDFs = %v, want only the readable accelerator", got)
	}
}

// TestEnumerateQAICMissingSysfsIsNotAnError matches the Source contract:
// hardware that is not there, on a host without the bus path at all, is the
// ordinary case and must not be reported as a failure.
func TestEnumerateQAICMissingSysfsIsNotAnError(t *testing.T) {
	socs, err := enumerateQAIC(filepath.Join(t.TempDir(), "nonexistent"))
	if err != nil {
		t.Fatalf("enumerateQAIC() error = %v, want nil", err)
	}
	if len(socs) != 0 {
		t.Fatalf("got %d SoCs from a missing sysfs", len(socs))
	}
}
