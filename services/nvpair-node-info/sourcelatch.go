// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

package main

import "sync"

// sourceLatch records which telemetry sources have failed and gone quiet.
//
// A source that cannot be reached — a missing nvidia-smi, an accelerator whose
// device node will not open — is latched on its first failure so the collector
// stops retrying it every tick and warns about it exactly once. The state is
// per source rather than global: one vendor's tooling being absent says nothing
// about another's, and a single shared flag would silence every source the
// moment the first one failed.
//
// The zero value is ready to use; the map is created on first latch so the
// type can sit as a plain field on a struct built with a literal.
//
// Latches are permanent for the life of the process, matching the behaviour
// this replaced. A source that recovers is not re-detected until restart.
//
// This file carries no build tag on purpose: the repository has no CI and no
// GOOS test matrix, so logic behind a tag is effectively untested logic.
type sourceLatch struct {
	mu     sync.Mutex
	failed map[string]bool
}

// Source names used with sourceLatch. They appear in operator-facing warnings,
// so they name the tooling rather than the vendor's marketing.
const (
	sourceNvidiaSmi = "nvidia-smi"
)

// latch marks source as unavailable and reports whether this call was the one
// that latched it. Exactly one caller receives true, so the caller can log the
// failure once without coordinating with anyone else.
func (l *sourceLatch) latch(source string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.failed[source] {
		return false
	}
	if l.failed == nil {
		l.failed = make(map[string]bool)
	}
	l.failed[source] = true
	return true
}

// latched reports whether source has already failed and should be skipped.
func (l *sourceLatch) latched(source string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.failed[source]
}
