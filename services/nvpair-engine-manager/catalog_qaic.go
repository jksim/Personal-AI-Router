// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// The Qualcomm engine's catalogue is a committed list of QPCs from the
// ZentreeLabs catalog, one per model, so the cluster's model id stays the
// Hugging Face id. Each entry was downloaded once to pin its SHA-256, because
// the catalog publishes no checksums. The batch, context and prefill values
// come from the QPC's qconfig.json; vLLM must be launched with the same ones.

//go:embed catalog/qaic-models.json
var qaicCatalogJSON []byte

// qaicCatalogSDK is the SDK version of the pinned container image. A QPC only
// runs on the SDK it was compiled with.
const qaicCatalogSDK = "1.21.6"

const qaicCatalogSource = "ZentreeLabs QPC catalog (SDK " + qaicCatalogSDK + "), SHA-256 pinned"

type qaicCatalogEntry struct {
	Model        string `json:"model"`
	QPC          string `json:"qpc"`
	URL          string `json:"url"`
	SHA256       string `json:"sha256"`
	Size         uint64 `json:"size"`
	UnpackedSize uint64 `json:"unpacked_size"`
	SDK          string `json:"sdk"`
	Devices      int    `json:"devices"`
	Batch        int    `json:"batch"`
	Context      int    `json:"context"`
	Prefill      int    `json:"prefill"`
}

var (
	qaicCatalogOnce    sync.Once
	qaicCatalogParsed  []qaicCatalogEntry
	qaicCatalogLoadErr error
)

// qaicCatalogEntries parses the embedded list once.
func qaicCatalogEntries() ([]qaicCatalogEntry, error) {
	qaicCatalogOnce.Do(func() {
		if err := json.Unmarshal(qaicCatalogJSON, &qaicCatalogParsed); err != nil {
			qaicCatalogLoadErr = fmt.Errorf("parse embedded qaic catalog: %w", err)
		}
	})
	return qaicCatalogParsed, qaicCatalogLoadErr
}

// usableQPCs keeps the QPCs this host can run: no more devices than it has SoCs,
// and a multi-SoC QPC only when the SoCs can reach each other. QPCs that use more
// of the card come first.
func usableQPCs(entries []qaicCatalogEntry, socCount int, p2pAllowed bool) []qaicCatalogEntry {
	var usable []qaicCatalogEntry
	for _, e := range entries {
		if e.Devices > socCount || (e.Devices > 1 && !p2pAllowed) {
			continue
		}
		usable = append(usable, e)
	}
	sort.SliceStable(usable, func(i, j int) bool { return usable[i].Devices > usable[j].Devices })
	return usable
}

// qaicCatalogModels shapes QPCs for engine:catalog.
func qaicCatalogModels(entries []qaicCatalogEntry) []CatalogModel {
	models := make([]CatalogModel, 0, len(entries))
	for _, e := range entries {
		author, _, _ := strings.Cut(e.Model, "/")
		models = append(models, CatalogModel{
			ID:     e.Model,
			Name:   e.Model,
			Author: author,
			URL:    "https://huggingface.co/" + e.Model,
			Size:   e.Size,
			Tags:   []string{fmt.Sprintf("%d SoC", e.Devices)},
		})
	}
	return models
}
