// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

package openaiproxy

import (
	"strings"
	"testing"

	"nvpair-shared/noderec"
)

// testProfile is the engine identity the package's own tests run against. It is
// deliberately not any shipped engine's profile: a test that passes only for
// MAX's port or another engine's service key would be testing the profile rather than
// the proxy.
func testProfile() Profile {
	return Profile{
		Engine: "testengine",
		// Borrowed rather than invented: a shipped engine's key is added in
		// its own task, and these tests care that the key is threaded, not
		// which key it is.
		ServiceKey:         noderec.ServiceLMStudio,
		DefaultPort:        1234,
		PortFile:           "testengine-proxy-port.json",
		LogName:            "testengine-proxy",
		ErrorIDPrefix:      "testengine-proxy",
		InferenceEndpoints: OpenAIInferenceEndpoints(),
		ModelListRoutes:    OpenAIModelListRoutes(),
		FacadeMessage:      "the compatibility facade is not a testengine engine",
	}
}

// TestProfileValidateRejectsIncompleteProfiles pins that a profile missing any
// load-bearing field is refused at startup rather than producing a proxy that
// looks healthy and behaves wrongly.
//
// None of these fail loudly at runtime: an empty engine tag silently
// unattributes every workload, an unset service key advertises under the zero
// key, a shared port file makes two engines fight over one file, and a missing
// error prefix collides with another engine's sticky error ids.
func TestProfileValidateRejectsIncompleteProfiles(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Profile)
		wantErr string
	}{
		{name: "complete profile is accepted", mutate: func(*Profile) {}},
		{name: "no engine", mutate: func(p *Profile) { p.Engine = "" }, wantErr: "engine name"},
		{name: "no service key", mutate: func(p *Profile) { p.ServiceKey = "" }, wantErr: "service key"},
		{name: "no port", mutate: func(p *Profile) { p.DefaultPort = 0 }, wantErr: "default port"},
		{name: "port out of range", mutate: func(p *Profile) { p.DefaultPort = 70000 }, wantErr: "default port"},
		{name: "no port file", mutate: func(p *Profile) { p.PortFile = "" }, wantErr: "port file"},
		{name: "no log name", mutate: func(p *Profile) { p.LogName = "" }, wantErr: "log name"},
		{name: "no error prefix", mutate: func(p *Profile) { p.ErrorIDPrefix = "" }, wantErr: "error id prefix"},
		{name: "no inference endpoints", mutate: func(p *Profile) { p.InferenceEndpoints = nil }, wantErr: "inference endpoints"},
		{name: "no model-list routes", mutate: func(p *Profile) { p.ModelListRoutes = nil }, wantErr: "model-list routes"},
		{name: "no facade message", mutate: func(p *Profile) { p.FacadeMessage = "" }, wantErr: "facade rejection"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			profile := testProfile()
			c.mutate(&profile)
			err := profile.Validate()
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() accepted a profile with no %s", c.wantErr)
			}
			if !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("Validate() = %q, want it to mention %q", err, c.wantErr)
			}
		})
	}
}

// TestProfileErrorIDIsPerEngine pins that two engines cannot collide on a
// sticky error id for the same node. nvpair-errors matches these ids literally,
// so a collision would let one engine clear another's error.
func TestProfileErrorIDIsPerEngine(t *testing.T) {
	max := testProfile()
	max.ErrorIDPrefix = "max-proxy"
	other := testProfile()
	other.ErrorIDPrefix = "other-proxy"

	const node = "node-a"
	if max.upstreamUnreachableID(node) == other.upstreamUnreachableID(node) {
		t.Fatal("two engines produced the same sticky error id for one node")
	}
	if got := max.upstreamUnreachableID(node); got != "max-proxy:upstream-unreachable:node-a" {
		t.Fatalf("error id = %q", got)
	}
}

// TestProfileInferenceRequestClassification pins which requests are reported as
// cluster workloads. A GET to an inference path is not a workload, and a POST
// to an unlisted path is forwarded without being counted as one.
func TestProfileInferenceRequestClassification(t *testing.T) {
	profile := testProfile()
	cases := []struct {
		method, path string
		want         bool
	}{
		{"POST", "/v1/chat/completions", true},
		{"POST", "/v1/completions", true},
		{"POST", "/v1/embeddings", true},
		{"GET", "/v1/chat/completions", false},
		{"POST", "/v1/models", false},
		{"POST", "/api/chat", false},
	}
	for _, c := range cases {
		if got := profile.isInferenceRequest(c.method, c.path); got != c.want {
			t.Fatalf("isInferenceRequest(%s %s) = %v, want %v", c.method, c.path, got, c.want)
		}
	}
}
