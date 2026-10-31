// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

package accel

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// switchedUltraFixture lays out sysfs as jksim-lnx2 has it: four SoCs, each
// behind its own downstream port of one PMC-Sierra switch. bus/pci/devices
// holds symlinks into the device tree, as on a real host.
func switchedUltraFixture(t *testing.T, hwmon map[string]string) string {
	t.Helper()
	root := t.TempDir()
	write := func(path, content string) {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	link := func(bdf, treePath string) {
		dir := filepath.Join(root, "bus", "pci", "devices")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(root, treePath), filepath.Join(dir, bdf)); err != nil {
			t.Fatal(err)
		}
	}
	tree := "devices/pci0000:c9/0000:c9:01.0/0000:ca:00.0"
	for i, port := range []string{"cb:00.0", "cb:01.0", "cb:02.0", "cb:03.0"} {
		soc := []string{"cc:00.0", "cd:00.0", "ce:00.0", "cf:00.0"}[i]
		portPath := tree + "/0000:" + port
		socPath := portPath + "/0000:" + soc
		write(portPath+"/vendor", "0x11f8\n")
		write(portPath+"/device", "0x4052\n")
		write(socPath+"/vendor", "0x17cb\n")
		write(socPath+"/device", "0xa100\n")
		if name, ok := hwmon["0000:"+soc]; ok {
			write(socPath+"/hwmon/hwmon"+string(rune('7'+i))+"/name", name+"\n")
		}
		link("0000:"+port, portPath)
		link("0000:"+soc, socPath)
		nodeDir := filepath.Join(root, "class", "accel", "accel"+string(rune('0'+i)))
		if err := os.MkdirAll(nodeDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(root, socPath), filepath.Join(nodeDir, "device")); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func allQAIC() map[string]string {
	return map[string]string{
		"0000:cc:00.0": "qaic", "0000:cd:00.0": "qaic", "0000:ce:00.0": "qaic", "0000:cf:00.0": "qaic",
	}
}

func TestInspectQAICReportsEachSoCAndItsSwitchPort(t *testing.T) {
	root := switchedUltraFixture(t, allQAIC())

	socs, err := inspectQAIC(root, "/dev/accel", ultraStatus)
	if err != nil {
		t.Fatalf("inspectQAIC() error = %v", err)
	}
	if len(socs) != 4 {
		t.Fatalf("got %d SoCs, want 4: %+v", len(socs), socs)
	}
	first := socs[0]
	if first.PCIAddress != "0000:cc:00.0" || first.Node != "accel0" {
		t.Fatalf("first SoC = %+v, want 0000:cc:00.0 on accel0", first)
	}
	if !first.Answered || first.QueryError != nil || !first.HasHwmon {
		t.Fatalf("first SoC = %+v, want answered with a qaic hwmon", first)
	}
	if first.UpstreamPort != "0000:cb:00.0" || first.UpstreamID != "11f8:4052" {
		t.Fatalf("upstream = %q %q, want 0000:cb:00.0 11f8:4052", first.UpstreamPort, first.UpstreamID)
	}
}

func TestInspectQAICKeepsTheReasonASoCDidNotAnswer(t *testing.T) {
	root := switchedUltraFixture(t, allQAIC())
	denied := func(string) (qaicStatus, error) { return qaicStatus{}, fs.ErrPermission }

	socs, err := inspectQAIC(root, "/dev/accel", denied)
	if err != nil {
		t.Fatalf("inspectQAIC() error = %v", err)
	}
	for _, soc := range socs {
		if soc.Answered || !errors.Is(soc.QueryError, fs.ErrPermission) {
			t.Fatalf("SoC = %+v, want not answered, with the permission error kept", soc)
		}
	}
}

func TestInspectQAICNeedsAHwmonNamedQaic(t *testing.T) {
	root := switchedUltraFixture(t, map[string]string{"0000:cc:00.0": "qaic", "0000:cd:00.0": "nvme"})

	socs, err := inspectQAIC(root, "/dev/accel", ultraStatus)
	if err != nil {
		t.Fatalf("inspectQAIC() error = %v", err)
	}
	got := []bool{socs[0].HasHwmon, socs[1].HasHwmon, socs[2].HasHwmon}
	if got[0] != true || got[1] != false || got[2] != false {
		t.Fatalf("HasHwmon = %v, want only the first SoC (name qaic)", got)
	}
}

func TestInspectQAICNoHardware(t *testing.T) {
	socs, err := inspectQAIC(t.TempDir(), "/dev/accel", ultraStatus)
	if err != nil || len(socs) != 0 {
		t.Fatalf("inspectQAIC() = %+v, %v; want nothing and no error", socs, err)
	}
}
