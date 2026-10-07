// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

/**
 * An engine the UI offers a model hub for must be able to pull a model.
 *
 * `EngineCapabilities[engine].engineHub` is what puts a browsable model list and
 * a download control in front of the user. The manifest is what makes the
 * download work. Nothing in the type system connects them, so MAX shipped with
 * a hub, a catalog, and no `pull_model` action — the models were listed, and
 * choosing one failed with "engine has no action pull_model". This test is that
 * connection.
 */

import { describe, expect, it } from 'vitest'
import fs from 'node:fs'
import path from 'node:path'
import type { EngineType } from '@/shared/types/engines'
import { EngineCapabilities } from '@/ui/constants/engine-capabilities'

const MANIFEST_DIR = path.resolve(process.cwd(), '../services/nvpair-engine-manager/manifests')

const ENGINE_TYPE_BY_MANIFEST_ID: Record<string, EngineType> = {
    ollama: 'ollama',
    lmstudio: 'lm-studio',
    max: 'max'
}

interface Manifest {
    engine: string
    actions?: Record<string, unknown>
}

function readManifests(): Manifest[] {
    return fs
        .readdirSync(MANIFEST_DIR)
        .filter(name => name.endsWith('.json'))
        .map(name => JSON.parse(fs.readFileSync(path.join(MANIFEST_DIR, name), 'utf8')) as Manifest)
}

describe('engine hub and pull_model agree', () => {
    it('every engine with a hub can pull a model', () => {
        for (const manifest of readManifests()) {
            const engineType = ENGINE_TYPE_BY_MANIFEST_ID[manifest.engine]
            expect(
                engineType,
                `manifest "${manifest.engine}" is not mapped, so it is exempt from this check`
            ).toBeDefined()

            const caps = EngineCapabilities[engineType]
            if (!caps.engineHub) continue
            expect(
                manifest.actions?.pull_model,
                `${manifest.engine} offers a model hub (${caps.engineHub.label}) but declares no pull_model action, so downloading from it fails`
            ).toBeDefined()
        }
    })

    it('every engine that can delete a model declares the action', () => {
        for (const manifest of readManifests()) {
            const engineType = ENGINE_TYPE_BY_MANIFEST_ID[manifest.engine]
            if (!engineType) continue
            if (!EngineCapabilities[engineType].hasDeleteModel) continue
            expect(
                manifest.actions?.delete_model,
                `${manifest.engine} advertises delete in the UI but declares no delete_model action`
            ).toBeDefined()
        }
    })
})
