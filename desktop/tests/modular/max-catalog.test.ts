// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

/**
 * The MAX catalog must only offer models MAX can actually serve.
 *
 * The manifest launches `max serve` with no task flag, which means text
 * generation. An embedding checkpoint offered here is a model the engine cannot
 * load: it exits during startup with "Missing required weights: lm_head.weight"
 * and PAIR reports the engine as having crashed — which is exactly what a user
 * hit after clicking load on Qwen/Qwen3-Embedding-8B.
 */

import { describe, expect, it } from 'vitest'
import { loadMaxModels } from '@/electron/model-hub/max-library'

/** Names that mark a checkpoint as something other than a chat model. */
const NOT_TEXT_GENERATION =
    /(embedding|embed|sentence-transformers|reranker|rerank|flux|diffusion|whisper|clip)/i

describe('MAX model catalog', () => {
    const models = loadMaxModels()

    it('is not empty', () => {
        expect(models.length).toBeGreaterThan(0)
    })

    it('offers only text-generation models', () => {
        const offenders = models.filter(m => NOT_TEXT_GENERATION.test(m.id))
        expect(
            offenders.map(m => m.id),
            'these cannot be served by `max serve` without a task flag, so loading one crashes the engine'
        ).toEqual([])
    })

    it('every entry is a usable HuggingFace repo id', () => {
        for (const model of models) {
            // hf rejects anything else, and the failure surfaces far from here.
            expect(model.id, `"${model.id}" is not <owner>/<name>`).toMatch(
                /^[A-Za-z0-9._-]+\/[A-Za-z0-9._-]+$/
            )
            expect(model.id.length).toBeLessThanOrEqual(96)
            expect(model.name, `${model.id} has no name`).toBeTruthy()
            expect(model.url).toBe(`https://huggingface.co/${model.id}`)
        }
    })
})
