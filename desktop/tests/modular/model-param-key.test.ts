// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

/**
 * The key the desktop sends must be the key the manifest's action reads.
 *
 * A command action templates a placeholder — `hf download {model}` — so it needs
 * `model`. An HTTP action posts a body its API parses, and Ollama's `/api/pull`
 * reads `name`. Sending the wrong one does not fail loudly: the engine-manager
 * substitutes an empty string and the engine rejects it far downstream. MAX
 * shipped that way and reported "Repo id must use alphanumeric chars … : ''".
 */

import { describe, expect, it } from 'vitest'
import fs from 'node:fs'
import path from 'node:path'

const MANIFEST_DIR = path.resolve(process.cwd(), '../services/nvpair-engine-manager/manifests')
const SUPERVISOR = path.resolve(process.cwd(), 'src/electron/service-bridge/modular-supervisor.ts')

interface ManifestAction {
    cmd?: string[]
    http?: { method?: string; path?: string }
}

interface Manifest {
    engine: string
    actions?: Record<string, ManifestAction>
}

function readManifests(): Manifest[] {
    return fs
        .readdirSync(MANIFEST_DIR)
        .filter(name => name.endsWith('.json'))
        .map(name => JSON.parse(fs.readFileSync(path.join(MANIFEST_DIR, name), 'utf8')) as Manifest)
}

/** The key the desktop actually sends, read out of MODEL_PARAM_KEY. */
function declaredParamKeys(): Record<string, string> {
    const source = fs.readFileSync(SUPERVISOR, 'utf8')
    const start = source.indexOf('const MODEL_PARAM_KEY')
    expect(start, 'MODEL_PARAM_KEY not found in modular-supervisor.ts').toBeGreaterThan(-1)
    const block = source.slice(start, source.indexOf('}', start))
    const out: Record<string, string> = {}
    for (const m of block.matchAll(/(\w+):\s*'(model|name)'/g)) out[m[1]] = m[2]
    return out
}

describe('model action param key', () => {
    it('matches what each manifest action reads', () => {
        const declared = declaredParamKeys()
        for (const manifest of readManifests()) {
            const pull = manifest.actions?.pull_model
            if (!pull) continue

            // A command templates {model}; an HTTP body carries the API's own key.
            const wanted = pull.cmd?.some(arg => arg.includes('{model}')) ? 'model' : 'name'
            expect(
                declared[manifest.engine],
                `${manifest.engine} has no MODEL_PARAM_KEY entry, so the desktop sends the fallback key and the engine receives an empty model`
            ).toBeDefined()
            expect(
                declared[manifest.engine],
                `${manifest.engine}'s pull_model reads "${wanted}" but the desktop sends "${declared[manifest.engine]}"`
            ).toBe(wanted)
        }
    })
})
