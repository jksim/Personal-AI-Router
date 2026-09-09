// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"
)

// TestExitMessageCarriesTheEngineReason covers the difference between an error
// a user can act on and one they cannot.
//
// "max exited unexpectedly" is the same sentence whether the model cannot be
// served, a GPU library is missing, or the machine ran out of memory. The engine
// prints which of those it was, immediately before dying, and that line was
// being discarded.
func TestExitMessageCarriesTheEngineReason(t *testing.T) {
	logs := newLogBuffer()
	logs.append("stdout", "Building, compiling, and initializing model...")
	logs.append("stderr", "Traceback (most recent call last):")
	logs.append("stderr", "ValueError: load_state_dict() strict=True validation failed. Missing required weights: lm_head.weight")
	logs.append("stderr", "Worker crashed (1), shutting down...")

	got := exitMessage("max", logs)
	if !strings.HasPrefix(got, "max exited unexpectedly") {
		t.Fatalf("message = %q, want it to still say the engine exited", got)
	}
	// The useful line is the diagnosis, not the crash notice that follows it.
	// "Worker crashed (1), shutting down" says only what PAIR already said; the
	// ValueError names the model's actual problem, which is what a user needs to
	// pick a different one.
	if !strings.Contains(got, "Missing required weights: lm_head.weight") {
		t.Fatalf("message = %q, want the engine's own diagnosis", got)
	}
}

// TestExitMessageWithNothingToSay keeps the message honest when the engine died
// silently — inventing a cause would be worse than admitting there is none.
func TestExitMessageWithNothingToSay(t *testing.T) {
	logs := newLogBuffer()
	logs.append("stdout", "Server ready")
	if got := exitMessage("max", logs); got != "max exited unexpectedly" {
		t.Fatalf("message = %q, want the bare statement", got)
	}
	if got := exitMessage("max", nil); got != "max exited unexpectedly" {
		t.Fatalf("message with no buffer = %q", got)
	}
}

func TestLastErrorLine(t *testing.T) {
	cases := []struct {
		name  string
		lines []string
		want  string
	}{
		{name: "no failure lines", lines: []string{"starting", "ready"}, want: ""},
		{
			// Engines print a traceback and then a summary; the later line is
			// closer to the cause than the first.
			name:  "prefers the last diagnosis",
			lines: []string{"ERROR: first problem", "ERROR: second problem"},
			want:  "ERROR: second problem",
		},
		{name: "ignores blank lines", lines: []string{"FATAL: boom", "   "}, want: "FATAL: boom"},
		{name: "matches an abort", lines: []string{"ABORT: symbol not found: cublasCreate_v2"}, want: "ABORT: symbol not found: cublasCreate_v2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logs := newLogBuffer()
			for _, l := range tc.lines {
				logs.append("stderr", l)
			}
			if got := lastErrorLine(logs.snapshot()); got != tc.want {
				t.Fatalf("lastErrorLine() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestLastErrorLineIsBounded stops a runaway log line becoming the whole error.
func TestLastErrorLineIsBounded(t *testing.T) {
	logs := newLogBuffer()
	logs.append("stderr", "ERROR: "+strings.Repeat("x", 5000))
	got := lastErrorLine(logs.snapshot())
	if len(got) > 320 {
		t.Fatalf("reason is %d chars, want it truncated", len(got))
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("truncated reason %q should show it was cut", got[len(got)-20:])
	}
}
