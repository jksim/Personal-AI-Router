// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

// Package accel discovers the compute accelerators attached to a host.
//
// Detection is a chain of independent sources rather than a single vendor
// query. A host may carry an NVIDIA GPU and a Qualcomm accelerator at once, and
// one vendor's tooling being absent or broken says nothing about another's, so
// no source may abort the chain or suppress its peers.
//
// The package carries no build tag. The repository has no CI and no GOOS test
// matrix, so logic behind a tag is effectively untested logic; only the thin
// exec and syscall shims a source needs belong in tagged files.
package accel

import (
	"context"
	"fmt"
)

// Device is one accelerator as reported by a source.
//
// The fields are deliberately the ones the node-info wire format already
// carries. Describing an accelerator more richly — vendor, device kind, a
// stable hardware id, per-reading validity — is a wire-format change and lands
// with that change, not here.
type Device struct {
	// Name is the human-facing model name, e.g. "NVIDIA RTX A4500".
	Name string

	// VramBytes is total on-device memory, or 0 when the source cannot
	// determine it.
	VramBytes uint64

	// StatsKey joins this device to a dynamic reading from the stats
	// collector. Its form is source-specific and opaque to everything else;
	// an empty key means the device has no dynamic readings at all.
	StatsKey string

	// UsesSystemMemory marks a unified-memory device whose memory usage is
	// the host's, not a separate pool.
	UsesSystemMemory bool

	// Vendor is a lowercase token identifying who made the device.
	Vendor string

	// Kind separates a compute accelerator from a display adapter. Only the
	// former is known to run inference, and the distinction cannot be made
	// from a device name.
	Kind string

	// Load is how busy the device is, when the source can measure it. It is
	// internal to detection rather than part of the wire format: the wire
	// carries a utilization percentage, and not every accelerator has one.
	Load Load
}

// Vendor tokens.
const (
	VendorNVIDIA   = "nvidia"
	VendorQualcomm = "qualcomm"
)

// Device kinds.
const (
	// KindGPU is a graphics processor that also runs compute.
	KindGPU = "gpu"

	// KindAccelerator is a compute-only device with no display output.
	KindAccelerator = "accelerator"

	// KindDisplay is an adapter found only by display-adapter enumeration.
	// Nothing is known about its compute capability, so it must not be
	// advertised as inference-ready.
	KindDisplay = "display"
)

// Load is how busy a device is, as a fraction in [0,1].
//
// Valid is separate from Fraction on purpose. Sources differ in what they can
// measure, and one of them — Qualcomm's — returns zero from a failed sensor
// read while still reporting success, so a bare float cannot distinguish "idle"
// from "no reading". The scheduler treats those very differently: an idle node
// attracts work and an unmeasured one is held at neutral pressure. Anything
// that cannot be measured must say so rather than defaulting to zero.
type Load struct {
	Fraction float64
	Valid    bool
}

// Source detects one class of accelerator. Implementations report only the
// devices they own, so a host with several kinds of hardware yields all of
// them.
type Source interface {
	// Name identifies the source in logs. It names the tooling being used
	// rather than the vendor, because that is what an operator has to go
	// and check.
	Name() string

	// Detect returns the devices this source owns. Hardware simply not
	// being present is the ordinary case and must return no devices and no
	// error; an error means the source itself malfunctioned.
	Detect(ctx context.Context) ([]Device, error)
}

// DeviceSample is a dynamic reading for one device previously detected.
type DeviceSample struct {
	// StatsKey matches the Device the reading belongs to.
	StatsKey string

	// Load is occupancy, if the source can measure it.
	Load Load

	// MemoryUsedBytes is device memory in use, zero when unknown.
	MemoryUsedBytes uint64
}

// Sampler is implemented by sources that can report changing readings, not
// just enumerate hardware. It is optional: a source that only enumerates is
// still useful, it simply contributes no telemetry.
type Sampler interface {
	Source

	// Sample reads current values. Hardware that has gone away yields no
	// samples and no error.
	Sample(ctx context.Context) ([]DeviceSample, error)
}

// Detect consults every source in order and returns their devices grouped in
// that order, along with one annotated error per source that failed.
//
// A failing source never aborts the chain: the caller gets whatever the other
// sources found. Callers decide what to do with the errors — the collector
// latches the source and warns once rather than logging every tick.
func Detect(ctx context.Context, sources []Source) ([]Device, []error) {
	var devices []Device
	var errs []error

	for _, source := range sources {
		found, err := source.Detect(ctx)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", source.Name(), err))
			continue
		}
		devices = append(devices, found...)
	}
	return devices, errs
}
