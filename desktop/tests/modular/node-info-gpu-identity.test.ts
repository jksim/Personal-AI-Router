// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it, vi } from 'vitest'

vi.mock('electron', () => ({ BrowserWindow: { getAllWindows: () => [] } }))
vi.mock('@/electron/window', () => ({ createOverviewWindow: vi.fn() }))

import { getModularBridgeState } from '@/electron/service-bridge/modular-state'

function discoverNode(state: ReturnType<typeof getModularBridgeState>, hostUuid: string): void {
    state.handleNotification({
        source: 'broker',
        method: 'discovery:nodes-changed',
        params: {
            nodes: [{ hostUuid, name: 'gpu-identity-host', ipAddress: '192.0.2.60', port: 14318 }]
        }
    })
}

// Accelerator identity used to be an array index, and the array was sorted so
// that anything named 'nvidia' came first. Both are gone: the node reports a
// stable device_id and the order it reports is the order shown.
describe('node-info accelerator identity', () => {
    it('keys devices by the id the node reports, not by array position', () => {
        const state = getModularBridgeState()
        discoverNode(state, 'uuid-gpuid-1')

        state.mergeNodeInfoResponse('uuid-gpuid-1', {
            hostUuid: 'uuid-gpuid-1',
            GPUs: [
                {
                    name: 'Qualcomm Cloud AI PCIe Ultra',
                    vram_bytes: 137438953472,
                    device_id: 'qaic:serial:ULTRA-0001',
                    vendor: 'qualcomm',
                    kind: 'accelerator'
                },
                {
                    name: 'NVIDIA RTX A4500',
                    vram_bytes: 21464350720,
                    device_id: 'GPU-94aef013',
                    vendor: 'nvidia',
                    kind: 'gpu'
                }
            ]
        })

        const gpus = state.getNodesInitial().nodes['uuid-gpuid-1'].topology.gpus
        expect(gpus.map(gpu => gpu.id)).toEqual([
            'uuid-gpuid-1:gpu:qaic:serial:ULTRA-0001',
            'uuid-gpuid-1:gpu:GPU-94aef013'
        ])
    })

    it('does not rank an accelerator behind a display adapter', () => {
        const state = getModularBridgeState()
        discoverNode(state, 'uuid-gpuid-2')

        // Reported accelerator first, display adapter second. The old
        // substring sort hoisted anything named 'nvidia', which put an
        // integrated display adapter ahead of the compute device — and index 0
        // drives the node card's primary ring.
        state.mergeNodeInfoResponse('uuid-gpuid-2', {
            hostUuid: 'uuid-gpuid-2',
            GPUs: [
                {
                    name: 'Qualcomm Cloud AI PCIe Ultra',
                    vram_bytes: 137438953472,
                    device_id: 'qaic:serial:ULTRA-0002',
                    kind: 'accelerator'
                },
                { name: 'NVIDIA display adapter', vram_bytes: 0, kind: 'display' }
            ]
        })

        const gpus = state.getNodesInitial().nodes['uuid-gpuid-2'].topology.gpus
        expect(gpus[0].name).toBe('Qualcomm Cloud AI PCIe Ultra')
    })

    it('keeps ids stable when a device is added, so history is not regrafted', () => {
        const state = getModularBridgeState()
        discoverNode(state, 'uuid-gpuid-3')

        state.mergeNodeInfoResponse('uuid-gpuid-3', {
            hostUuid: 'uuid-gpuid-3',
            GPUs: [{ name: 'NVIDIA RTX A4500', vram_bytes: 21464350720, device_id: 'GPU-a4500' }]
        })
        const before = state.getNodesInitial().nodes['uuid-gpuid-3'].topology.gpus[0].id

        // An accelerator is installed and now enumerates ahead of the GPU.
        state.mergeNodeInfoResponse('uuid-gpuid-3', {
            hostUuid: 'uuid-gpuid-3',
            GPUs: [
                { name: 'Qualcomm Cloud AI PCIe Ultra', vram_bytes: 137438953472, device_id: 'qaic:serial:U3' },
                { name: 'NVIDIA RTX A4500', vram_bytes: 21464350720, device_id: 'GPU-a4500' }
            ]
        })

        const after = state.getNodesInitial().nodes['uuid-gpuid-3'].topology.gpus
        const a4500 = after.find(gpu => gpu.name === 'NVIDIA RTX A4500')
        expect(a4500?.id).toBe(before)
        // Under positional ids the A4500 would now be index 1 and would have
        // inherited whatever history index 1 previously held.
        expect(a4500?.id).not.toBe('uuid-gpuid-3:gpu:1')
    })

    it('falls back to array position for a peer that reports no device id', () => {
        const state = getModularBridgeState()
        discoverNode(state, 'uuid-gpuid-4')

        state.mergeNodeInfoResponse('uuid-gpuid-4', {
            hostUuid: 'uuid-gpuid-4',
            GPUs: [{ name: 'NVIDIA RTX A4500', vram_bytes: 21464350720 }]
        })

        const gpus = state.getNodesInitial().nodes['uuid-gpuid-4'].topology.gpus
        expect(gpus[0].id).toBe('uuid-gpuid-4:gpu:0')
    })

    it('carries inference_hardware_ids through to the topology', () => {
        const state = getModularBridgeState()
        discoverNode(state, 'uuid-gpuid-5')

        state.mergeNodeInfoResponse('uuid-gpuid-5', {
            hostUuid: 'uuid-gpuid-5',
            GPUs: [
                { name: 'Qualcomm Cloud AI PCIe Ultra', device_id: 'qaic:serial:U5', kind: 'accelerator' },
                { name: 'Some display adapter', kind: 'display' }
            ],
            inference_hardware_ids: ['qaic:serial:U5']
        })

        const topology = state.getNodesInitial().nodes['uuid-gpuid-5'].topology
        expect(topology.inferenceHardwareIds).toEqual(['qaic:serial:U5'])
    })
})
