// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

import { EnabledEngineTypes } from '@/shared/constants/engines'
import { EngineType } from '@/shared/types/engines'
import { useEngineStatusStore } from '@/ui/stores/engine-status.store'
import { useConnectionStore } from '@/ui/stores/connection.store'
import { useNodesStore } from '@/ui/stores/nodes.store'
import { usePendingActionsStore } from '@/ui/stores/pending-actions.store'
import { getEnginesForNode } from '@/ui/utils/get-engines-for-node'
import { inlineEngineEntries } from '@/ui/utils/inline-engines'
import { Button, Flex, Switch, Text } from '@nvidia/foundations-react-core'
import { Download } from '@/ui/components/icons'
import { useCallback, useMemo } from 'react'

export default function NodeEnginesInline({ nodeId }: { nodeId: string }) {
    const statusByNode = useEngineStatusStore(s => s.statusByNode)
    const isRemote = useConnectionStore(s => s.selfId !== nodeId)
    const isDisconnected = useNodesStore(s => isRemote && s.nodes.get(nodeId)?.status === 'offline')
    // Re-render when a lifecycle command for this node begins/clears; the
    // per-engine pending state is read below via getState().
    const lifecyclePendingFingerprint = usePendingActionsStore(state => {
        const parts: string[] = []
        for (const [key, p] of state.pending) {
            if (p.nodeId === nodeId && key.endsWith(':lifecycle')) {
                parts.push(`${p.engineType}:${p.action}`)
            }
        }
        return parts.join('|')
    })
    void lifecyclePendingFingerprint
    const selfEngines = useMemo(
        () => getEnginesForNode(nodeId ?? '', statusByNode, EnabledEngineTypes),
        [nodeId, statusByNode]
    )

    const nodeOs = useNodesStore(state => state.nodes.get(nodeId)?.os)
    const targetOs = nodeOs ?? window.windowApi.platform
    const allBackends = useMemo(
        () => inlineEngineEntries(selfEngines, targetOs),
        [selfEngines, targetOs]
    )

    const handleToggle = useCallback(
        (engineType: EngineType) => {
            window.pairApi.engines.toggle(engineType, nodeId)
        },
        [nodeId]
    )

    const handleInstall = useCallback(
        (engineType: EngineType) => {
            window.pairApi.engines.install(engineType, nodeId)
        },
        [nodeId]
    )

    return (
        <>
            {allBackends.map(b => {
                const pending = Boolean(
                    usePendingActionsStore.getState().getLifecyclePending(nodeId, b.type)
                )
                const isTransitioning =
                    pending ||
                    b.status === 'installing' ||
                    b.status === 'uninstalling' ||
                    b.status === 'starting' ||
                    b.status === 'stopping'

                if (b.status === 'not-installed' && !pending) {
                    return (
                        <Button
                            key={b.name}
                            kind="secondary"
                            size="tiny"
                            className="shrink-0 no-drag-elements px-2"
                            onClick={e => {
                                e.preventDefault()
                                e.stopPropagation()
                                handleInstall(b.type)
                            }}
                            disabled={isRemote && isDisconnected}
                            aria-label={`Install ${b.name}`}
                        >
                            <Download style={{ fontSize: 14 }} />
                            <Text kind="body/regular/sm" className="ml-1">
                                {b.name}
                            </Text>
                        </Button>
                    )
                }

                return (
                    <Flex
                        key={b.name}
                        align="center"
                        gap="2"
                        className="shrink-0 no-drag-elements pair-engines-inline"
                    >
                        <Flex align="center" justify="center" className="h-4 w-7 shrink-0 -mr-1">
                            {isTransitioning ? (
                                <span
                                    className="spinner-element-medium"
                                    role="status"
                                    aria-label=""
                                />
                            ) : (
                                <Switch
                                    size="small"
                                    checked={b.status === 'running'}
                                    onCheckedChange={() => handleToggle(b.type)}
                                    disabled={isRemote && isDisconnected}
                                    title={
                                        isRemote && isDisconnected
                                            ? 'Node is disconnected'
                                            : `${b.status === 'running' ? 'Stop' : 'Start'} ${b.name}`
                                    }
                                    aria-label={
                                        isRemote && isDisconnected
                                            ? 'Node is disconnected'
                                            : `${b.status === 'running' ? 'Stop' : 'Start'} ${b.name}`
                                    }
                                />
                            )}
                        </Flex>
                        <Text
                            kind="body/regular/sm"
                            className="cursor-pointer"
                            onClick={() => (!isTransitioning ? handleToggle(b.type) : undefined)}
                        >
                            {b.name}
                        </Text>
                    </Flex>
                )
            })}
        </>
    )
}
