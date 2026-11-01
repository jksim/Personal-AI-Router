// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from 'vitest'

import { EnabledEngineTypes } from '@/shared/constants/engines'
import type { EngineStatusData } from '@/shared/types/engines'
import { inlineEngineEntries } from '@/ui/utils/inline-engines'

type Reported = Pick<EngineStatusData, 'engineType' | 'processStatus'>

const notInstalledEverywhere: Reported[] = EnabledEngineTypes.map(engineType => ({
    engineType,
    processStatus: 'not-installed'
}))

describe('node card engine chips', () => {
    it('offers no install chip for an engine that cannot run on the node OS', () => {
        const types = inlineEngineEntries(notInstalledEverywhere, 'Windows').map(e => e.type)
        expect(types).not.toContain('qaic')
        expect(types).not.toContain('max')
        expect(types).toEqual(['ollama', 'lm-studio', 'llama-cpp'])
    })

    it('offers every install chip on Linux', () => {
        const types = inlineEngineEntries(notInstalledEverywhere, 'Linux').map(e => e.type)
        expect(types).toEqual(['ollama', 'lm-studio', 'llama-cpp', 'max', 'qaic'])
    })

    it('still shows an engine the node already runs', () => {
        const running: Reported[] = [{ engineType: 'qaic', processStatus: 'running' }]
        expect(inlineEngineEntries(running, 'Windows').map(e => e.type)).toEqual(['qaic'])
    })
})
