// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from 'vitest'

import type { PlatformDisplayName } from '@/shared/types/platform'
import { offersManualDownloadForOs } from '@/ui/utils/backend-target-os'

const otherOses: PlatformDisplayName[] = ['Windows', 'MacOS']

describe('engine download link', () => {
    it('offers no Download for an engine that cannot run on the node OS', () => {
        for (const os of otherOses) {
            expect(offersManualDownloadForOs('qaic', os)).toBe(false)
            expect(offersManualDownloadForOs('max', os)).toBe(false)
        }
    })

    it('offers no Download where PAIR installs the engine itself', () => {
        expect(offersManualDownloadForOs('qaic', 'Linux')).toBe(false)
        expect(offersManualDownloadForOs('ollama', 'Windows')).toBe(false)
    })
})
