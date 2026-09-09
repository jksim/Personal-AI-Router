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
 * itself reports as supported (`max list`, which prints example repo ids per
 * architecture), filtered to text-generation models a user would plausibly run
 * on one machine. Image, video and experimental checkpoints are excluded.
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
