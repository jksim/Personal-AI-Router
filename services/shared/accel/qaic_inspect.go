// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

package accel

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// QAICSoCState is what a host-readiness check needs to know about one SoC.
type QAICSoCState struct {
	PCIAddress string
	// Node is the accel device node name, such as "accel0". Empty when the
	// driver created none.
	Node string
	// Answered reports whether the SoC returned its status. QueryError says
	// why it did not; it is nil when it answered or had no node to query.
	Answered   bool
	QueryError error
	// HasHwmon reports a hwmon named "qaic" under the SoC, which is what the
	// fan control on a host reads.
	HasHwmon bool
	// UpstreamPort is the PCI address of the bridge port above the SoC, and
	// UpstreamID its "vendor:device" pair. Both are empty at the top of a tree.
	UpstreamPort string
	UpstreamID   string
}

// InspectQAIC describes every Qualcomm SoC on this host.
func InspectQAIC() ([]QAICSoCState, error) {
	return inspectQAIC(DefaultSysfsRoot, DefaultDevRoot, queryQAICStatus)
}

func inspectQAIC(sysfsRoot, devRoot string, query func(string) (qaicStatus, error)) ([]QAICSoCState, error) {
	socs, err := enumerateQAIC(sysfsRoot)
	if err != nil || len(socs) == 0 {
		return nil, err
	}
	nodes, err := mapAccelNodes(sysfsRoot)
	if err != nil {
		nodes = map[string]string{}
	}
	states := make([]QAICSoCState, 0, len(socs))
	for _, soc := range socs {
		state := QAICSoCState{PCIAddress: soc.BDF, Node: nodes[soc.BDF]}
		if state.Node != "" {
			if _, err := query(filepath.Join(devRoot, state.Node)); err == nil {
				state.Answered = true
			} else {
				state.QueryError = err
			}
		}
		device := filepath.Join(sysfsRoot, "bus", "pci", "devices", soc.BDF)
		if dir, ok := findHwmonDir(filepath.Join(device, "hwmon")); ok {
			name, err := os.ReadFile(filepath.Join(dir, "name"))
			state.HasHwmon = err == nil && strings.TrimSpace(string(name)) == "qaic"
		}
		state.UpstreamPort, state.UpstreamID = upstreamPort(device)
		states = append(states, state)
	}
	return states, nil
}

// upstreamPort returns the bridge directly above a PCI device, read from the
// resolved sysfs path, whose parent directory is that bridge.
func upstreamPort(device string) (string, string) {
	resolved, err := filepath.EvalSymlinks(device)
	if err != nil {
		return "", ""
	}
	parent := filepath.Dir(resolved)
	address := filepath.Base(parent)
	if strings.Count(address, ":") != 2 {
		return "", ""
	}
	vendor, ok := readPCIID(filepath.Join(parent, "vendor"))
	if !ok {
		return address, ""
	}
	id, ok := readPCIID(filepath.Join(parent, "device"))
	if !ok {
		return address, ""
	}
	return address, fmt.Sprintf("%04x:%04x", vendor, id)
}
