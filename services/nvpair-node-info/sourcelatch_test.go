// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"sync"
	"testing"
)

// TestSourceLatchWarnsOnce pins the property the previous single atomic.Bool
// provided: the first failure reports true so the caller logs, and every later
// failure reports false so a missing binary does not warn on every tick.
func TestSourceLatchWarnsOnce(t *testing.T) {
	var latch sourceLatch

	if latch.latched("nvidia") {
		t.Fatal("a source that has never failed reports as latched")
	}
	if !latch.latch("nvidia") {
		t.Fatal("first failure did not claim the warning")
	}
	if !latch.latched("nvidia") {
		t.Fatal("source is not latched after failing")
	}
	for i := 0; i < 3; i++ {
		if latch.latch("nvidia") {
			t.Fatalf("failure %d claimed the warning again", i+2)
		}
	}
}

// TestSourceLatchIsolatesSources is the whole point of the change. The
// collector previously held one process-lifetime bool, so the first nvidia-smi
// miss silenced GPU collection entirely and no second source could ever live
// behind it. Latching one source must leave every other source untouched.
func TestSourceLatchIsolatesSources(t *testing.T) {
	var latch sourceLatch

	latch.latch("nvidia")

	if !latch.latched("nvidia") {
		t.Fatal("nvidia should be latched")
	}
	if latch.latched("qaic") {
		t.Fatal("latching nvidia also latched qaic")
	}
	if !latch.latch("qaic") {
		t.Fatal("qaic could not claim its own first warning")
	}
	if !latch.latched("nvidia") || !latch.latched("qaic") {
		t.Fatal("latching qaic disturbed nvidia's state")
	}
}

// TestSourceLatchZeroValueUsable keeps the type usable as a plain field on
// statsCollector, which is built with a struct literal and has no constructor
// to thread a map through.
func TestSourceLatchZeroValueUsable(t *testing.T) {
	type holder struct{ latch sourceLatch }
	var h holder

	if h.latch.latched("nvidia") {
		t.Fatal("zero value reports a source as latched")
	}
	if !h.latch.latch("nvidia") {
		t.Fatal("zero value did not accept a latch")
	}
}

// TestSourceLatchConcurrentLatchWarnsOnce pins that exactly one caller is told
// to warn. decodeGPU runs on the ticker goroutine today, but the chain this
// latch exists to enable will sample sources concurrently, and a latch that
// let two callers through would produce duplicate warnings for one failure.
func TestSourceLatchConcurrentLatchWarnsOnce(t *testing.T) {
	var latch sourceLatch

	const goroutines = 64
	var start sync.WaitGroup
	var done sync.WaitGroup
	var mu sync.Mutex
	claimed := 0

	start.Add(1)
	for i := 0; i < goroutines; i++ {
		done.Add(1)
		go func() {
			defer done.Done()
			start.Wait()
			if latch.latch("nvidia") {
				mu.Lock()
				claimed++
				mu.Unlock()
			}
		}()
	}
	start.Done()
	done.Wait()

	if claimed != 1 {
		t.Fatalf("%d goroutines claimed the warning, want exactly 1", claimed)
	}
}
