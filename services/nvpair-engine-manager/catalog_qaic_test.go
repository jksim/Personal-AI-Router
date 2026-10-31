// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/url"
	"regexp"
	"testing"
)

var qaicCatalogHosts = map[string]bool{
	"dc00tk1pxen80.cloudfront.net":                          true,
	"qualcom-qpc-models.s3-accelerate.amazonaws.com":        true,
	"qualcom-qpc-models.s3-website-us-east-1.amazonaws.com": true,
}

func TestQAICCatalogEntriesAreComplete(t *testing.T) {
	entries, err := qaicCatalogEntries()
	if err != nil {
		t.Fatalf("qaicCatalogEntries() error = %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("the QPC catalog is empty")
	}
	sha := regexp.MustCompile(`^[0-9a-f]{64}$`)
	repo := regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)
	seen := map[string]bool{}
	for _, e := range entries {
		u, err := url.Parse(e.URL)
		if err != nil || u.Scheme != "https" || !qaicCatalogHosts[u.Host] {
			t.Errorf("%s: URL %q is not https on a catalog host", e.Model, e.URL)
		}
		if !repo.MatchString(e.Model) {
			t.Errorf("model %q is not a Hugging Face repo id", e.Model)
		}
		if seen[e.Model] {
			t.Errorf("%s: more than one QPC; the model id must name one QPC", e.Model)
		}
		seen[e.Model] = true
		if !sha.MatchString(e.SHA256) {
			t.Errorf("%s: SHA-256 %q is not 64 lowercase hex digits", e.Model, e.SHA256)
		}
		if e.Devices < 1 || e.Batch < 1 || e.Context < 1 || e.Prefill < 1 || e.Size == 0 || e.UnpackedSize == 0 {
			t.Errorf("%s: devices, batch, context, prefill and sizes must all be set: %+v", e.Model, e)
		}
		if e.SDK != qaicCatalogSDK {
			t.Errorf("%s: SDK %q, want %q to match the pinned container", e.Model, e.SDK, qaicCatalogSDK)
		}
	}
}

func syntheticQPCs() []qaicCatalogEntry {
	return []qaicCatalogEntry{
		{Model: "a/one-soc", Devices: 1},
		{Model: "a/four-soc", Devices: 4},
		{Model: "a/two-soc", Devices: 2},
	}
}

func modelsOf(entries []qaicCatalogEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Model)
	}
	return out
}

func TestQAICUsableQPCsPutsWholeCardFirst(t *testing.T) {
	got := modelsOf(usableQPCs(syntheticQPCs(), 4, true))
	want := []string{"a/four-soc", "a/two-soc", "a/one-soc"}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("usable = %v, want %v", got, want)
	}
}

func TestQAICUsableQPCsNeverOffersMoreDevicesThanSoCs(t *testing.T) {
	for _, e := range usableQPCs(syntheticQPCs(), 2, true) {
		if e.Devices > 2 {
			t.Fatalf("a %d-device QPC (%s) was offered to a 2-SoC host", e.Devices, e.Model)
		}
	}
}

func TestQAICUsableQPCsWithoutP2POffersOneSoCOnly(t *testing.T) {
	got := modelsOf(usableQPCs(syntheticQPCs(), 4, false))
	if len(got) != 1 || got[0] != "a/one-soc" {
		t.Fatalf("usable = %v, want only the 1-SoC QPC when SoCs cannot reach each other", got)
	}
}

func TestQAICCatalogModelsUseTheModelID(t *testing.T) {
	models := qaicCatalogModels(usableQPCs([]qaicCatalogEntry{{Model: "Qwen/Qwen3-30B-A3B", Devices: 4, Size: 46394016830}}, 4, true))
	if len(models) != 1 || models[0].ID != "Qwen/Qwen3-30B-A3B" || models[0].Author != "Qwen" || models[0].Size != 46394016830 {
		t.Fatalf("models = %+v, want the Hugging Face id, its author and the download size", models)
	}
}

func readyHost(socs int, p2p bool) func() qaicHostReady {
	return func() qaicHostReady {
		return qaicHostReady{CardPresent: true, CardAnswered: true, NodesOpen: true, DockerUsable: true, SoCCount: socs, P2PAllowed: p2p}
	}
}

func TestQAICEngineCatalogOffersWhatTheHostCanRun(t *testing.T) {
	c := newCatalogService()
	c.qaicHost = readyHost(4, true)

	result, err := c.Catalog(context.Background(), "qaic", "linux", "amd64", "")
	if err != nil {
		t.Fatalf("Catalog() error = %v", err)
	}
	found := false
	for _, m := range result.Models {
		found = found || m.ID == "Qwen/Qwen3-30B-A3B"
	}
	if !found || result.Source != qaicCatalogSource {
		t.Fatalf("result = %+v, want the 4-SoC Qwen3 QPC from the pinned catalog", result)
	}
}

func TestQAICEngineCatalogOnASmallerHost(t *testing.T) {
	c := newCatalogService()
	c.qaicHost = readyHost(2, true)

	result, err := c.Catalog(context.Background(), "qaic", "linux", "amd64", "")
	if err != nil {
		t.Fatalf("Catalog() error = %v", err)
	}
	for _, m := range result.Models {
		if m.ID == "Qwen/Qwen3-30B-A3B" {
			t.Fatal("a 4-SoC QPC was offered to a 2-SoC host")
		}
	}
}

func TestQAICEngineCatalogIsLinuxOnly(t *testing.T) {
	c := newCatalogService()
	c.qaicHost = readyHost(4, true)

	result, err := c.Catalog(context.Background(), "qaic", "windows", "amd64", "")
	if err != nil {
		t.Fatalf("Catalog() error = %v", err)
	}
	if len(result.Models) != 0 {
		t.Fatalf("models = %+v, want none: the qaic driver is Linux only", result.Models)
	}
}
