// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"reflect"
	"strings"
	"testing"
)

// TestFixedArgsSubstitutesManagedValues covers the defect a live start found.
//
// FixedArgs is a template like the rest of the runtime, and it is compared
// against a command whose placeholders are already resolved. An engine whose
// fixed prefix names its model therefore matched nothing and was refused with
// "launch does not match its fixed startup arguments" — after a sixteen-minute
// install, at the moment of first start.
func TestFixedArgsSubstitutesManagedValues(t *testing.T) {
	policy := &EditableLaunch{FixedArgs: []string{"serve", "--model", "{model}"}}
	got := policy.fixedArgs(Runtime{Model: "modularai/Llama-3.1-8B"})
	want := []string{"serve", "--model", "modularai/Llama-3.1-8B"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fixedArgs() = %q, want %q", got, want)
	}

	// Nothing selected yet is an empty argument, not a literal placeholder: the
	// start path refuses an unselected model with a message that says so, and a
	// stray "{model}" on the command line would not.
	if got := policy.fixedArgs(Runtime{}); got[2] != "" {
		t.Fatalf("fixedArgs() with no model = %q, want the value empty", got)
	}

	if policy.fixedArgs(Runtime{}) == nil && len(policy.FixedArgs) > 0 {
		t.Fatal("fixedArgs() dropped the prefix")
	}
	var absent *EditableLaunch
	if got := absent.fixedArgs(Runtime{}); got != nil {
		t.Fatalf("fixedArgs() on no policy = %q, want nil", got)
	}
}

// TestEffectiveLaunchAcceptsAManagedModel is the end of that path: the resolved
// command must pass the recheck that runs immediately before the engine starts.
func TestEffectiveLaunchAcceptsAManagedModel(t *testing.T) {
	rt := Runtime{
		Bin:   "/opt/max/bin/max",
		Model: "modularai/Llama-3.1-8B",
		EditableLaunch: &EditableLaunch{
			FixedArgs: []string{"serve", "--model", "{model}"},
			Controls: []LaunchControl{
				{Value: "{server.port}", Flags: []string{"--port"}},
			},
		},
	}
	command := launchCommand{
		Bin:  rt.Bin,
		Args: []string{"serve", "--model", "modularai/Llama-3.1-8B", "--port", "8001"},
	}
	if err := validateEffectiveLaunch(rt, command, "127.0.0.1", "8001"); err != nil {
		t.Fatalf("validateEffectiveLaunch() = %v, want the managed model accepted", err)
	}

	// A command naming a different model than the one selected is still
	// refused: the prefix is managed, so it is not a place to smuggle a value.
	tampered := launchCommand{Bin: rt.Bin, Args: []string{"serve", "--model", "someone/else", "--port", "8001"}}
	err := validateEffectiveLaunch(rt, tampered, "127.0.0.1", "8001")
	if err == nil || !strings.Contains(err.Error(), "fixed startup arguments") {
		t.Fatalf("validateEffectiveLaunch() on a swapped model = %v, want a refusal", err)
	}
}
