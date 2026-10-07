// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"regexp"
	"testing"
)

// notTextGeneration marks a checkpoint as something other than a chat model.
// `max serve` without a task flag cannot load one, and offering it is how a user
// clicked load on Qwen/Qwen3-Embedding-8B and saw the engine crash.
var notTextGeneration = regexp.MustCompile(`(?i)(embedding|embed|sentence-transformers|reranker|rerank|flux|diffusion|whisper|clip)`)

var hfRepoID = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)

func TestMaxCatalogOffersOnlyServableModels(t *testing.T) {
	result, err := newCatalogService().Catalog(context.Background(), "max", "linux", "amd64", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Models) == 0 {
		t.Fatal("MAX catalog is empty")
	}
	for _, m := range result.Models {
		if notTextGeneration.MatchString(m.ID) {
			t.Errorf("%s cannot be served by `max serve` without a task flag", m.ID)
		}
		// hf download rejects anything else, and the failure surfaces far away.
		if !hfRepoID.MatchString(m.ID) || len(m.ID) > 96 {
			t.Errorf("%q is not an <owner>/<name> repo id", m.ID)
		}
		if m.Name != m.ID || m.URL != "https://huggingface.co/"+m.ID {
			t.Errorf("%s: name %q / url %q are not pull-ready", m.ID, m.Name, m.URL)
		}
	}
}
