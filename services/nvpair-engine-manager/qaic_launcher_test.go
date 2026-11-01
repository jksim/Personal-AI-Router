// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runLauncher runs launchers/qaic-serve.sh against a fake host. The SoCs'
// accel nodes are numbered out of PCI order on purpose, as the driver may
// number them. It returns what the fake docker was asked to run.
func runLauncher(t *testing.T, model, launchArgs string) (string, error) {
	t.Helper()
	root := t.TempDir()
	sysfs := filepath.Join(root, "sys")
	dev := filepath.Join(root, "dev")
	for node, bdf := range map[string]string{"accel0": "0000:cd:00.0", "accel1": "0000:cc:00.0", "accel2": "0000:cf:00.0", "accel3": "0000:ce:00.0"} {
		dir := filepath.Join(sysfs, "class", "accel", node)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("../../../devices/pci0000:c9/"+bdf, filepath.Join(dir, "device")); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(dev, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dev, node), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	models := filepath.Join(root, "models")
	qpc := filepath.Join(models, "Example/Tiny/qpc_config/qpc-abc/qpc")
	if err := os.MkdirAll(qpc, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(qpc, "programqpc.bin"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(models, "Example/Tiny/qpc_config/launch.args"), []byte(launchArgs+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(root, "bin")
	record := filepath.Join(root, "docker.log")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	fake := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + record + "\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "launchers/qaic-serve.sh", model)
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"),
		"QAIC_HOST=127.0.0.1", "QAIC_PORT=8101", "QAIC_MODELS_DIR="+models, "QAIC_INSTALL_DIR="+root,
		"QAIC_SYSFS_ROOT="+sysfs, "QAIC_DEV_ROOT="+dev)
	out, err := cmd.CombinedOutput()
	data, _ := os.ReadFile(record)
	if err != nil {
		return string(data), &exec.ExitError{ProcessState: cmd.ProcessState, Stderr: out}
	}
	var run string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "run ") {
			run = line
		}
	}
	return strings.ReplaceAll(run, dev+"/", "/dev/accel/"), nil
}

func TestQaicLauncherMountsOnlyTheDeviceGroup(t *testing.T) {
	run, err := runLauncher(t, "Example/Tiny", "--device-group 2 --max-model-len 8192")
	if err != nil {
		t.Fatalf("launcher: %v", err)
	}
	// SoC 2 in PCI order is 0000:ce:00.0, which the driver named accel3.
	if strings.Count(run, "--device ") != 1 || !strings.Contains(run, "--device /dev/accel/accel3") {
		t.Fatalf("docker run = %q, want only SoC 2's node, accel3", run)
	}
	if !strings.Contains(run, "--device-group 0 ") {
		t.Fatalf("docker run = %q, want the group renumbered from 0 inside the container", run)
	}
}

func TestQaicLauncherMountsAFourSoCGroupInOrder(t *testing.T) {
	run, err := runLauncher(t, "Example/Tiny", "--device-group 0,1,2,3 --max-model-len 8192")
	if err != nil {
		t.Fatalf("launcher: %v", err)
	}
	want := "--device /dev/accel/accel1:/dev/accel/accel1 --device /dev/accel/accel0:/dev/accel/accel0 --device /dev/accel/accel3:/dev/accel/accel3 --device /dev/accel/accel2:/dev/accel/accel2"
	if !strings.Contains(run, want) || !strings.Contains(run, "--device-group 0,1,2,3 ") {
		t.Fatalf("docker run = %q, want the nodes in PCI order: %s", run, want)
	}
}

func TestQaicLauncherRefusesAModelPathOutsideTheModels(t *testing.T) {
	if _, err := runLauncher(t, "../../etc", "--device-group 0"); err == nil {
		t.Fatal("the launcher accepted a model id with ..")
	}
}
