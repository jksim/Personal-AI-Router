// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"io/fs"
	"strings"
	"testing"

	"nvpair-shared/accel"
)

// The command line of jksim-lnx2 after the ACS parameter was added.
const lnx2Cmdline = "BOOT_IMAGE=/boot/vmlinuz-7.0.0-38-generic root=UUID=570cc5af ro quiet splash " +
	"pci=disable_acs_redir=0000:cb:00.0;0000:cb:01.0;0000:cb:02.0;0000:cb:03.0 vt.handoff=7"

func ultraSoCs(n int) []accel.QAICSoCState {
	socs := make([]accel.QAICSoCState, 0, n)
	for i := 0; i < n; i++ {
		socs = append(socs, accel.QAICSoCState{
			PCIAddress:   "0000:c" + string(rune('c'+i)) + ":00.0",
			Node:         "accel" + string(rune('0'+i)),
			Answered:     true,
			HasHwmon:     true,
			UpstreamPort: "0000:cb:0" + string(rune('0'+i)) + ".0",
			UpstreamID:   "11f8:4052",
		})
	}
	return socs
}

func blocking(problems []qaicHostProblem) []qaicHostProblem {
	var out []qaicHostProblem
	for _, p := range problems {
		if p.Blocking {
			out = append(out, p)
		}
	}
	return out
}

func TestQAICHostReadyWithFourSoCsAndACSOff(t *testing.T) {
	ready, problems := checkQAICHost(ultraSoCs(4), nil, lnx2Cmdline, nil)

	if !ready.ok() || ready.SoCCount != 4 || !ready.P2PAllowed {
		t.Fatalf("ready = %+v, want ok with 4 SoCs and P2P allowed", ready)
	}
	if len(problems) != 0 {
		t.Fatalf("problems = %+v, want none", problems)
	}
}

func TestQAICHostNoCard(t *testing.T) {
	ready, problems := checkQAICHost(nil, nil, lnx2Cmdline, nil)

	if ready.ok() || ready.CardPresent {
		t.Fatalf("ready = %+v, want no card", ready)
	}
	if b := blocking(problems); len(b) != 1 || !strings.Contains(b[0].Message, "No Qualcomm Cloud AI 100") {
		t.Fatalf("problems = %+v, want one blocking 'no card' problem", problems)
	}
}

func TestQAICHostCardThatDidNotAnswer(t *testing.T) {
	socs := ultraSoCs(4)
	socs[2].Answered = false
	socs[2].QueryError = errors.New("open /dev/accel/accel2: no such device")

	ready, problems := checkQAICHost(socs, nil, lnx2Cmdline, nil)

	if ready.ok() || ready.CardAnswered || ready.SoCCount != 3 {
		t.Fatalf("ready = %+v, want not answered, 3 SoCs counted", ready)
	}
	b := blocking(problems)
	if len(b) != 1 || !strings.Contains(b[0].Message, "0000:ce:00.0") || !strings.Contains(b[0].Fix, "powered on") {
		t.Fatalf("problems = %+v, want one naming the SoC and asking to power the card on", problems)
	}
}

func TestQAICHostNoHwmon(t *testing.T) {
	socs := ultraSoCs(4)
	socs[0].HasHwmon = false

	ready, problems := checkQAICHost(socs, nil, lnx2Cmdline, nil)

	if ready.ok() || ready.CardAnswered {
		t.Fatalf("ready = %+v, want not ready without the SoC's temperature sensor", ready)
	}
	if b := blocking(problems); len(b) != 1 || !strings.Contains(b[0].Message, "temperature") {
		t.Fatalf("problems = %+v, want one about the missing temperature sensor", problems)
	}
}

func TestQAICHostNodesNotReadable(t *testing.T) {
	socs := ultraSoCs(4)
	for i := range socs {
		socs[i].Answered = false
		socs[i].QueryError = fs.ErrPermission
	}

	ready, problems := checkQAICHost(socs, nil, lnx2Cmdline, nil)

	if ready.ok() || ready.NodesOpen {
		t.Fatalf("ready = %+v, want nodes not open", ready)
	}
	b := blocking(problems)
	if len(b) != 1 || !strings.Contains(b[0].Fix, "udev") {
		t.Fatalf("problems = %+v, want one problem whose fix is the udev rule", problems)
	}
}

func TestQAICHostNoDocker(t *testing.T) {
	ready, problems := checkQAICHost(ultraSoCs(4), nil, lnx2Cmdline, errors.New("permission denied while trying to connect to the Docker daemon socket"))

	if ready.ok() || ready.DockerUsable {
		t.Fatalf("ready = %+v, want Docker not usable", ready)
	}
	if b := blocking(problems); len(b) != 1 || !strings.Contains(b[0].Fix, "docker group") {
		t.Fatalf("problems = %+v, want one problem whose fix is the docker group", problems)
	}
}

func TestQAICHostACSRedirectOnLimitsToOneSoC(t *testing.T) {
	ready, problems := checkQAICHost(ultraSoCs(4), nil, "BOOT_IMAGE=/boot/vmlinuz ro quiet splash", nil)

	if !ready.ok() || ready.P2PAllowed {
		t.Fatalf("ready = %+v, want ready but without P2P", ready)
	}
	if len(blocking(problems)) != 0 || len(problems) != 1 || !strings.Contains(problems[0].Fix, "pci=disable_acs_redir=0000:cb:00.0;0000:cb:01.0;0000:cb:02.0;0000:cb:03.0") {
		t.Fatalf("problems = %+v, want one non-blocking problem giving the exact parameter", problems)
	}
}

func TestQAICHostACSParameterMustCoverEveryPort(t *testing.T) {
	cmdline := "ro pci=disable_acs_redir=0000:cb:00.0;0000:cb:01.0"

	ready, _ := checkQAICHost(ultraSoCs(4), nil, cmdline, nil)

	if ready.P2PAllowed {
		t.Fatal("P2P allowed with two of four ports listed")
	}
}

func TestQAICHostACSParameterByVendorDevice(t *testing.T) {
	cmdline := "ro pci=noaer,disable_acs_redir=pci:11f8:4052"

	ready, _ := checkQAICHost(ultraSoCs(4), nil, cmdline, nil)

	if !ready.P2PAllowed {
		t.Fatal("P2P not allowed although every port's vendor:device is listed")
	}
}

func TestQAICHostTwoSoCs(t *testing.T) {
	ready, _ := checkQAICHost(ultraSoCs(2), nil, "pci=disable_acs_redir=cb:00.0;cb:01.0", nil)

	if !ready.ok() || ready.SoCCount != 2 || !ready.P2PAllowed {
		t.Fatalf("ready = %+v, want 2 SoCs with P2P (addresses without a domain)", ready)
	}
}

func TestQAICHostOneSoCNeedsNoP2P(t *testing.T) {
	ready, problems := checkQAICHost(ultraSoCs(1), nil, "ro", nil)

	if !ready.ok() || !ready.P2PAllowed || len(problems) != 0 {
		t.Fatalf("ready = %+v, problems = %+v; one SoC sends nothing to another", ready, problems)
	}
}
