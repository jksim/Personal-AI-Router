// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

import type { CSSProperties } from 'react'
import { type EngineType } from '@/shared/types/engines'
import ollamaIcon from '@/ui/assets/engine-icons/ollama.png?inline'
import lmStudioIcon from '@/ui/assets/engine-icons/lm-studio.png?inline'

export default function EngineIcon({ type, size = 32 }: { type: EngineType; size?: number }) {
    const dimension = `${size}px`
    const imgStyle: CSSProperties = { width: '100%', height: '100%', objectFit: 'contain' }
    const containerStyle: CSSProperties = {
        width: dimension,
        minWidth: dimension,
        maxWidth: dimension,
        height: dimension,
        minHeight: dimension,
        maxHeight: dimension,
        backgroundColor: '#fff',
        borderRadius: '25%',
        overflow: 'hidden'
    }

    if (type === 'ollama') {
        return (
            <div style={containerStyle}>
                <img src={ollamaIcon} alt="Ollama" style={imgStyle} />
            </div>
        )
    }

    if (type === 'lm-studio') {
        imgStyle.objectFit = 'cover'

        return (
            <div style={containerStyle}>
                <img src={lmStudioIcon} alt="LM Studio" style={imgStyle} />
            </div>
        )
    }

    // A self-authored wordmark rather than the vendor's logo. The other two
    // icons are bundled vendor artwork; redistributing a third party's mark
    // from a fork is a trademark question this does not need to raise, and a
    // missing icon would render nothing at all.
    if (type === 'max') {
        return (
            <div style={{ ...containerStyle, backgroundColor: '#0b0b0f' }}>
                <svg viewBox="0 0 64 64" style={imgStyle} role="img" aria-label="MAX">
                    <title>MAX</title>
                    <rect width="64" height="64" fill="#0b0b0f" />
                    <text
                        x="32"
                        y="41"
                        textAnchor="middle"
                        fontFamily="system-ui, -apple-system, Segoe UI, Roboto, sans-serif"
                        fontSize="22"
                        fontWeight="700"
                        letterSpacing="1"
                        fill="#f5f5f7"
                    >
                        MAX
                    </text>
                </svg>
            </div>
        )
    }

    return null
}
