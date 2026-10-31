// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func gateExecutor(t *testing.T, host func() (qaicHostReady, []qaicHostProblem)) *Executor {
	t.Helper()
	e := NewExecutor(loadWithOverrides(t, t.TempDir()), NewReporter(nil), nil, filepath.Join(t.TempDir(), "engine-bin"))
	e.qaicHost = host
	return e
}

func readyFour() (qaicHostReady, []qaicHostProblem) {
	return qaicHostReady{CardPresent: true, CardAnswered: true, NodesOpen: true, DockerUsable: true, SoCCount: 4, P2PAllowed: true}, nil
}

func TestQaicInstallRefusedWhenTheCardDidNotAnswer(t *testing.T) {
	e := gateExecutor(t, func() (qaicHostReady, []qaicHostProblem) {
		return qaicHostReady{CardPresent: true, NodesOpen: true, DockerUsable: true},
			[]qaicHostProblem{{Message: "The card did not answer from 0000:ce:00.0.", Fix: "Make sure the card is powered on.", Blocking: true}}
	})

	err := e.Install(context.Background(), "qaic")
	if err == nil || !strings.Contains(err.Error(), "did not answer") || !strings.Contains(err.Error(), "powered on") {
		t.Fatalf("Install() error = %v, want the problem and its fix", err)
	}
	found := false
	for _, reported := range e.reporter.snapshot() {
		found = found || (reported.ID == installFailedID("qaic") && reported.EngineType == "qaic" && strings.Contains(reported.Message, "did not answer"))
	}
	if !found {
		t.Fatalf("reported = %+v, want an install failure for qaic", e.reporter.snapshot())
	}
}

func TestQaicGateIgnoresOtherEngines(t *testing.T) {
	e := gateExecutor(t, func() (qaicHostReady, []qaicHostProblem) {
		t.Fatal("the Qualcomm host check ran for another engine")
		return qaicHostReady{}, nil
	})
	if err := e.qaicGate("max", "HuggingFaceTB/SmolLM2-135M-Instruct"); err != nil {
		t.Fatalf("qaicGate(max) = %v, want nil", err)
	}
}

func TestQaicGateAcceptsAModelThatFits(t *testing.T) {
	e := gateExecutor(t, readyFour)
	if err := e.qaicGate("qaic", "Qwen/Qwen3-30B-A3B"); err != nil {
		t.Fatalf("qaicGate() = %v, want nil on a ready 4-SoC host", err)
	}
}

func TestQaicGateRefusesAModelNeedingMoreSoCs(t *testing.T) {
	e := gateExecutor(t, func() (qaicHostReady, []qaicHostProblem) {
		ready, _ := readyFour()
		ready.SoCCount = 2
		return ready, nil
	})
	err := e.qaicGate("qaic", "Qwen/Qwen3-30B-A3B")
	if err == nil || !strings.Contains(err.Error(), "4 SoCs") || !strings.Contains(err.Error(), "2") {
		t.Fatalf("qaicGate() = %v, want a refusal naming 4 SoCs needed and 2 present", err)
	}
}

func TestQaicGateRefusesAMultiSoCModelWithoutP2P(t *testing.T) {
	e := gateExecutor(t, func() (qaicHostReady, []qaicHostProblem) {
		ready, _ := readyFour()
		ready.P2PAllowed = false
		return ready, []qaicHostProblem{{Message: "The card's SoCs cannot reach each other.", Fix: "Add the kernel parameter pci=disable_acs_redir=0000:cb:00.0 and reboot."}}
	})
	err := e.qaicGate("qaic", "Qwen/Qwen3-30B-A3B")
	if err == nil || !strings.Contains(err.Error(), "disable_acs_redir") {
		t.Fatalf("qaicGate() = %v, want the ACS problem and its fix", err)
	}
}
