// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package main

import (
	"context"
	"errors"
	"testing"

	"nvpair-node-info/accel"
)

type fakeSampler struct {
	name    string
	samples []accel.DeviceSample
	err     error
	calls   int
}

func (f *fakeSampler) Name() string { return f.name }

func (f *fakeSampler) Detect(context.Context) ([]accel.Device, error) { return nil, nil }

func (f *fakeSampler) Sample(context.Context) ([]accel.DeviceSample, error) {
	f.calls++
	return f.samples, f.err
}

// TestSampleAcceleratorsStampsOnValidLoad is the change that lets a non-NVIDIA
// node be scheduled fairly. A source with an occupancy reading and no
// utilization percentage must count as a usable sample: previously only
// nvidia-smi could produce one, so such a node never set telemetryValid and sat
// at neutral pressure however idle it was.
func TestSampleAcceleratorsStampsOnValidLoad(t *testing.T) {
	c := &statsCollector{samplers: []accel.Sampler{&fakeSampler{
		name: "qaic",
		samples: []accel.DeviceSample{{
			StatsKey:        "qaic:serial:ULTRA-0001",
			Load:            accel.Load{Fraction: 0.25, Valid: true},
			MemoryUsedBytes: 8 << 30,
		}},
	}}}

	out := map[string]gpuStat{}
	if !c.sampleAccelerators(out) {
		t.Fatal("a valid occupancy reading did not count as a usable sample")
	}
	stat, ok := out["qaic:serial:ULTRA-0001"]
	if !ok {
		t.Fatalf("sample not folded in under its stats key: %v", out)
	}
	if stat.UtilizationPct != 25 {
		t.Fatalf("utilization = %d, want 25 from a 0.25 load", stat.UtilizationPct)
	}
	if stat.VRAMUsed != 8<<30 {
		t.Fatalf("memory used = %d, want the reported 8 GiB", stat.VRAMUsed)
	}
}

// TestSampleAcceleratorsIgnoresInvalidLoad keeps "we could not measure" out of
// the telemetry. Publishing it would set telemetryValid and advertise the node
// as idle, which is the opposite of the truth and attracts work.
func TestSampleAcceleratorsIgnoresInvalidLoad(t *testing.T) {
	c := &statsCollector{samplers: []accel.Sampler{&fakeSampler{
		name: "qaic",
		samples: []accel.DeviceSample{{
			StatsKey: "qaic:pci:0000:05:00.0",
			Load:     accel.Load{}, // unmeasured
		}},
	}}}

	out := map[string]gpuStat{}
	if c.sampleAccelerators(out) {
		t.Fatal("an unmeasured device counted as a usable sample")
	}
	if len(out) != 0 {
		t.Fatalf("an unmeasured device was published: %v", out)
	}
}

// TestSampleAcceleratorsLatchesFailingSource pins that a failing source is
// silenced after one warning, and — the point of the per-source latch — that it
// does not silence its peers.
func TestSampleAcceleratorsLatchesFailingSource(t *testing.T) {
	broken := &fakeSampler{name: "broken", err: errors.New("device unreadable")}
	healthy := &fakeSampler{
		name: "healthy",
		samples: []accel.DeviceSample{{
			StatsKey: "healthy:0",
			Load:     accel.Load{Fraction: 0.5, Valid: true},
		}},
	}
	c := &statsCollector{samplers: []accel.Sampler{broken, healthy}}

	for i := 0; i < 3; i++ {
		out := map[string]gpuStat{}
		if !c.sampleAccelerators(out) {
			t.Fatalf("tick %d: healthy source stopped contributing", i)
		}
		if _, ok := out["healthy:0"]; !ok {
			t.Fatalf("tick %d: healthy source lost its reading", i)
		}
	}

	if broken.calls != 1 {
		t.Fatalf("broken source was polled %d times, want 1 before latching", broken.calls)
	}
	if healthy.calls != 3 {
		t.Fatalf("healthy source was polled %d times, want 3", healthy.calls)
	}
}

// TestSampleAcceleratorsSkipsKeylessSamples guards the join. A reading with no
// stats key cannot be attached to any device, and folding it in under an empty
// key would attach it to whichever device also lacked one.
func TestSampleAcceleratorsSkipsKeylessSamples(t *testing.T) {
	c := &statsCollector{samplers: []accel.Sampler{&fakeSampler{
		name:    "qaic",
		samples: []accel.DeviceSample{{Load: accel.Load{Fraction: 0.9, Valid: true}}},
	}}}

	out := map[string]gpuStat{}
	if c.sampleAccelerators(out) {
		t.Fatal("a sample with no stats key counted as usable")
	}
	if len(out) != 0 {
		t.Fatalf("keyless sample published: %v", out)
	}
}

// TestSampleAcceleratorsNoSamplers is the state on every host today: NVIDIA
// only, nothing else registered. It must be a no-op rather than a stamp.
func TestSampleAcceleratorsNoSamplers(t *testing.T) {
	c := &statsCollector{}
	out := map[string]gpuStat{}
	if c.sampleAccelerators(out) {
		t.Fatal("no samplers produced a usable sample")
	}
}
