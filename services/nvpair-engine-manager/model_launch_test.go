// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

package main

import "testing"

// TestRuntimeNeedsModel pins how an engine declares itself model-configured:
// by templating {model} in a launch string, and by nothing else.
//
// Deriving it from the manifest rather than a separate flag means the two can
// never disagree — an engine that templates {model} but forgot to set a flag
// would otherwise start with a literal "{model}" on its command line.
func TestRuntimeNeedsModel(t *testing.T) {
	cases := []struct {
		name string
		rt   Runtime
		want bool
	}{
		{name: "no model anywhere", rt: Runtime{Bin: "ollama", Args: []string{"serve"}}},
		{
			name: "args template it",
			rt:   Runtime{Bin: "max", Args: []string{"serve", "--model", "{model}"}},
			want: true,
		},
		{
			name: "bin templates it",
			rt:   Runtime{Bin: "{install_dir}/{model}/bin/serve"},
			want: true,
		},
		{
			name: "env templates it",
			rt:   Runtime{Bin: "max", Env: map[string]string{"SELECTED": "{model}"}},
			want: true,
		},
		{
			name: "start command templates it",
			rt:   Runtime{Start: [][]string{{"svc", "start", "{model}"}}},
			want: true,
		},
		{
			name: "stop command templates it",
			rt:   Runtime{Bin: "max", Stop: &StopSpec{Cmd: []string{"pkill", "-f", "{model}"}}},
			want: true,
		},
		{
			// A different placeholder that merely contains the word is not
			// {model}; a substring match on "model" would wrongly claim this.
			name: "a similarly named flag does not count",
			rt:   Runtime{Bin: "max", Args: []string{"serve", "--model-override", "{port}"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runtimeNeedsModel(tc.rt); got != tc.want {
				t.Fatalf("runtimeNeedsModel() = %v, want %v", got, tc.want)
			}
		})
	}
}
