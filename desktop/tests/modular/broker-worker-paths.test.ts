// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

/**
 * Every broker-owned binary must be handed to the broker.
 *
 * The broker resolves an unflagged worker as a sibling of its own working
 * directory, which in a dev launch is the desktop package — where no service
 * binary lives. So a binary that is staged, declared broker-owned, and simply
 * never passed does not fail: the broker logs "path not resolved" and runs
 * without it. max-proxy shipped in exactly that state, staged into cli-bin and
 * invisible to the broker, and nothing caught it until the app was run.
 */

import { describe, expect, it } from 'vitest'
import fs from 'node:fs'
import path from 'node:path'
import { MODULAR_RUNTIME_BINARIES } from '@/shared/constants/modular-binaries'

const SUPERVISOR = path.resolve(process.cwd(), 'src/electron/service-bridge/modular-supervisor.ts')

/** The `passPath('--flag', 'process-name')` calls in brokerStartupArgs. */
function passedProcessNames(): Set<string> {
    const source = fs.readFileSync(SUPERVISOR, 'utf8')
    const start = source.indexOf('private brokerStartupArgs()')
    expect(start, 'brokerStartupArgs not found in modular-supervisor.ts').toBeGreaterThan(-1)
    const body = source.slice(start, source.indexOf('\n    }', start))
    return new Set(
        [...body.matchAll(/passPath\(\s*'(--[a-z-]+)'\s*,\s*'([a-z0-9-]+)'\s*\)/g)].map(m => m[2])
    )
}

describe('broker worker paths', () => {
    it('passes a path for every broker-owned binary', () => {
        const passed = passedProcessNames()
        const brokerOwned = MODULAR_RUNTIME_BINARIES.filter(b => b.launchOwner === 'broker').map(
            b => b.processName
        )
        expect(brokerOwned.length).toBeGreaterThan(0)
        for (const name of brokerOwned) {
            expect(
                passed.has(name),
                `${name} is broker-owned but brokerStartupArgs never passes its path, so the broker will run without it`
            ).toBe(true)
        }
    })

    it('passes a path only for binaries that exist', () => {
        const passed = passedProcessNames()
        const known = new Set<string>(MODULAR_RUNTIME_BINARIES.map(b => b.processName))
        for (const name of passed) {
            expect(known.has(name), `brokerStartupArgs passes unknown process "${name}"`).toBe(true)
        }
    })
})
