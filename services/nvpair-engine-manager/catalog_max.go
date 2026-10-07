// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// MAX's catalogue is a committed list, like Ollama's. MAX has no catalogue
// endpoint: it serves one model per process and exposes only what is loaded. The
// list is the HuggingFace repo ids MAX itself reports as supported
// (`max list --json`), restricted to architectures that generate text.
//
// The restriction is not cosmetic. The manifest launches `max serve` with no task
// flag, which means text generation, so an embedding checkpoint is a model the
// engine cannot load: it exits during startup with "Missing required weights:
// lm_head.weight". One architecture serves both tasks, so a checkpoint whose
// name marks it as an embedding, reranker or image model is excluded as well.
//
// It is not exhaustive. MAX accepts any supported repo id, and download-by-name
// takes one the list does not carry.

//go:embed catalog/max-models.json
var maxCatalogJSON []byte

// maxCatalogSource names where the list came from, for CatalogResult.Source.
const maxCatalogSource = "max list --json (MAX 26.5.0), text-generation architectures"

var (
	maxCatalogOnce   sync.Once
	maxCatalogModels []CatalogModel
	maxCatalogErr    error
)

// maxCatalog parses the embedded list once and serves it thereafter.
func maxCatalog() ([]CatalogModel, error) {
	maxCatalogOnce.Do(func() {
		var ids []string
		if err := json.Unmarshal(maxCatalogJSON, &ids); err != nil {
			maxCatalogErr = fmt.Errorf("parse embedded max catalog: %w", err)
			return
		}
		maxCatalogModels = make([]CatalogModel, 0, len(ids))
		for _, id := range ids {
			author, _, _ := strings.Cut(id, "/")
			maxCatalogModels = append(maxCatalogModels, CatalogModel{
				ID:     id,
				Name:   id,
				Author: author,
				URL:    "https://huggingface.co/" + id,
				Tags:   []string{},
			})
		}
	})
	return maxCatalogModels, maxCatalogErr
}
