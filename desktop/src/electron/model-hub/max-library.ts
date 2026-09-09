// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

import type { EngineHubModel } from '@/shared/types/engine-api'
import maxModels from './max-models.json'

/**
 * MAX's model hub is a committed list, like Ollama's, rather than a live
 * catalog fetch.
 *
 * MAX has no catalog endpoint to query: it serves one model per process and
 * exposes only what is loaded. The list is the set of HuggingFace repo ids MAX
 * itself reports as supported (`max list --json`), restricted to the
 * architectures that generate text.
 *
 * The restriction is not cosmetic. The manifest launches `max serve` with no
 * task flag, which means text generation, so offering an embedding checkpoint
 * hands the user a model the engine cannot load: it exits during startup with
 * "Missing required weights: lm_head.weight" and PAIR reports the engine as
 * having crashed. One architecture serves both tasks, so a checkpoint whose
 * name marks it as an embedding, reranker or image model is excluded as well.
 *
 * It is not exhaustive and is not meant to be — MAX accepts any supported
 * HuggingFace repo id, and a user can type one the list does not carry.
 */
export function loadMaxModels(): EngineHubModel[] {
    return (maxModels as string[]).map(toHubModel)
}

function toHubModel(id: string): EngineHubModel {
    const slash = id.indexOf('/')
    return {
        id,
        name: id,
        author: slash > 0 ? id.slice(0, slash) : '',
        url: `https://huggingface.co/${id}`,
        downloads: 0,
        likes: 0,
        updatedAt: '',
        tags: []
    }
}
