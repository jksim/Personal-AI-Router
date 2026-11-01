// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

import { EnabledEngineTypes, EngineDisplayNames } from '@/shared/constants/engines'
import type { EngineStatusData } from '@/shared/types/engines'
import type { PlatformDisplayName } from '@/shared/types/platform'
import { canAutoInstallBackendForOs } from '@/ui/utils/backend-target-os'

/**
 * The engines a node card shows inline. It shows every engine the node has
 * reported — not just ones with a proxy port. It skips the padded
 * `initializing` placeholder, so an engine a node never reports shows no
 * spinner. It also skips the install chip for an engine that cannot run on the
 * node's OS.
 */
export function inlineEngineEntries(
    engines: Pick<EngineStatusData, 'engineType' | 'processStatus'>[],
    targetOs: PlatformDisplayName
) {
    return EnabledEngineTypes.flatMap(type => {
        const backend = engines.find(b => b.engineType === type)
        if (!backend || backend.processStatus === 'initializing') {
            return []
        }
        if (
            backend.processStatus === 'not-installed' &&
            !canAutoInstallBackendForOs(type, targetOs)
        ) {
            return []
        }
        return [{ type, name: EngineDisplayNames[type], status: backend.processStatus }]
    })
}
