// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package main

import (
	"context"
	"log"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/jaypipes/ghw"

	"nvpair-node-info/accel"
)

// nvidiaSmiTimeout caps how long we wait for a single nvidia-smi invocation.
// The tool is normally well under 100 ms, but on a wedged driver it can hang;
// a hard ceiling keeps both the one-shot startup detect and the per-tick stats
// collector from blocking indefinitely.
const nvidiaSmiTimeout = 3 * time.Second

// detectGPUs enumerates accelerators on Linux by running the source chain and
// mapping what it finds onto the wire type. NVIDIA's source yields the
// marketing name, total VRAM, and a stable per-GPU UUID we reuse as the join
// key (statsKey) against the dynamic stats collector's snapshot. When no source
// reports anything — no NVIDIA driver, or an AMD/Intel-only host — it falls
// back to ghw, which reports adapter names but no VRAM and no join key, so
// those hosts list their GPUs without dynamic VRAM/utilization (matching the
// pre-existing non-Windows behavior).
//
// ghw enumerates display adapters, so it is a fallback for graphics cards
// rather than a way to find a compute accelerator; a source that owns the
// hardware belongs in the chain instead.
//
// On unified-memory architectures (UMA, e.g. Grace-Blackwell / DGX Spark)
// nvidia-smi reports [N/A] for memory.total because the GPU shares system
// DRAM; in that case VramBytes is filled from detectMemoryTotal() instead.
func detectGPUs() []GPUInfo {
	devices, errs := accel.Detect(context.Background(), linuxAccelSources())
	for _, err := range errs {
		// Debug, not warn: on a host with no NVIDIA driver this is the
		// ordinary case, and the stats collector already warns once about
		// the same missing binary. Two warnings for one absent tool is noise.
		slog.Debug("accelerator detection source failed", "err", err)
	}
	if len(devices) == 0 {
		return detectGPUsGHW()
	}

	gpus := make([]GPUInfo, 0, len(devices))
	for _, device := range devices {
		gpus = append(gpus, GPUInfo{
			Name:      device.Name,
			VramBytes: device.VramBytes,
			Vendor:    device.Vendor,
			Kind:      device.Kind,
			// DeviceID is the same opaque identity the stats collector
			// joins on; publishing it lets a consumer key per-device
			// state without inventing a positional index.
			DeviceID:              device.StatsKey,
			statsKey:              device.StatsKey,
			usesSystemMemoryUsage: device.UsesSystemMemory,
		})
	}
	return gpus
}

// linuxAccelSources is the detection chain, in reporting order. Qualcomm and
// any other accelerator source join this slice; nothing else has to change.
func linuxAccelSources() []accel.Source {
	return []accel.Source{nvidiaSource{}, accel.NewQAICSource()}
}

// nvidiaSource detects NVIDIA GPUs through nvidia-smi.
type nvidiaSource struct{}

func (nvidiaSource) Name() string { return sourceNvidiaSmi }

// Detect returns the NVIDIA GPUs nvidia-smi reports. A host with no NVIDIA
// driver yields no devices and no error — absent hardware is not a fault.
// Parsing yielding no rows is treated the same way, so the caller still falls
// back to ghw exactly as it did before the chain existed.
func (nvidiaSource) Detect(ctx context.Context) ([]accel.Device, error) {
	out, err := nvidiaSmiCSVContext(ctx, "uuid,name,memory.total")
	if err != nil {
		return nil, err
	}
	gpus, uma := parseNvidiaStatic(out)
	if len(gpus) == 0 {
		return nil, nil
	}
	// On unified-memory parts nvidia-smi reports [N/A] for memory.total
	// because the GPU shares system DRAM; fill it from the host's total.
	if uma {
		if total := detectMemoryTotal(); total > 0 {
			for i := range gpus {
				if gpus[i].usesSystemMemoryUsage {
					gpus[i].VramBytes = total
				}
			}
		}
	}

	devices := make([]accel.Device, 0, len(gpus))
	for _, gpu := range gpus {
		devices = append(devices, accel.Device{
			Name:             gpu.Name,
			VramBytes:        gpu.VramBytes,
			StatsKey:         gpu.statsKey,
			UsesSystemMemory: gpu.usesSystemMemoryUsage,
			Vendor:           accel.VendorNVIDIA,
			Kind:             accel.KindGPU,
		})
	}
	return devices, nil
}

// detectGPUsGHW is the ghw-based fallback, identical in spirit to the
// non-Windows/non-Linux path in gpu_other.go: enumerate display adapters and
// return names only (VramBytes stays 0, statsKey stays empty).
func detectGPUsGHW() []GPUInfo {
	gpu, err := ghw.GPU()
	if err != nil {
		log.Printf("GPU detection error: %v", err)
		return nil
	}
	var gpus []GPUInfo
	for _, card := range gpu.GraphicsCards {
		name := "Unknown"
		if card.DeviceInfo != nil && card.DeviceInfo.Product != nil {
			name = card.DeviceInfo.Product.Name
		}
		// ghw enumerates display adapters. Nothing here says the device
		// runs compute, so it is reported as a display adapter rather than
		// claimed as a GPU that could take inference work.
		gpus = append(gpus, GPUInfo{Name: name, Kind: accel.KindDisplay})
	}
	return gpus
}

// nvidiaSmiCSV runs `nvidia-smi --query-gpu=<fields> --format=csv,noheader,nounits`
// and returns raw stdout. The caller parses the comma-separated rows. A missing
// binary (not on PATH) surfaces as an exec error, which callers treat as "no
// NVIDIA GPU data available" and degrade silently.
func nvidiaSmiCSV(fields string) (string, error) {
	return nvidiaSmiCSVContext(context.Background(), fields)
}

// nvidiaSmiCSVContext is nvidiaSmiCSV under a caller-supplied parent context,
// so a shutting-down process can abort a query rather than wait out
// nvidiaSmiTimeout.
func nvidiaSmiCSVContext(parent context.Context, fields string) (string, error) {
	return nvidiaSmiCSVWithRunner(parent, nvidiaSmiTimeout, runNvidiaSmi, fields)
}

// runNvidiaSmi is the only place this file touches the process table. It is
// split out so nvidiaSmiCSVWithRunner stays testable: the exec boundary was
// previously unreachable from a test, which left the timeout, the degrade path,
// and the ghw fallback trigger unverified on Linux.
func runNvidiaSmi(ctx context.Context, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, "nvidia-smi", args...).Output()
}

// nvidiaSmiCSVWithRunner builds the query argv, bounds it with timeout, and
// hands it to run. Stdout is returned verbatim — including on error, because a
// partial read is still worth parsing — and the error is returned unwrapped,
// because decodeGPU logs it as-is.
func nvidiaSmiCSVWithRunner(
	parent context.Context,
	timeout time.Duration,
	run func(context.Context, ...string) ([]byte, error),
	fields string,
) (string, error) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	out, err := run(ctx,
		"--query-gpu="+fields,
		"--format=csv,noheader,nounits")
	return string(out), err
}

// isNvidiaSmiNA reports whether an nvidia-smi CSV field is a "not
// applicable" sentinel rather than a numeric value. UMA platforms such as
// DGX Spark return [N/A] or [Not Supported] for GPU memory queries.
func isNvidiaSmiNA(s string) bool {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "[]")
	switch strings.ToLower(s) {
	case "n/a", "not supported":
		return true
	default:
		return false
	}
}

// parseNvidiaStatic decodes the static query (uuid,name,memory.total) into
// GPUInfo records. memory.total is reported in MiB (because of -nounits); we
// convert to bytes. Rows missing a uuid or name, or with an unparseable VRAM
// figure, are skipped rather than emitted with placeholder values. Each
// unified-memory row is marked so response assembly can source its used bytes
// from system memory without depending on dynamic nvidia-smi collection. The
// second return value reports whether any row was unified, allowing the caller
// to fetch the shared system-memory total only when needed.
func parseNvidiaStatic(out string) ([]GPUInfo, bool) {
	var gpus []GPUInfo
	var unifiedMemory bool
	for _, line := range strings.Split(out, "\n") {
		fields := splitCSVRow(line)
		if len(fields) < 3 {
			continue
		}
		uuid, name := fields[0], fields[1]
		if uuid == "" || name == "" {
			continue
		}
		var vramBytes uint64
		usesUnifiedMemory := isNvidiaSmiNA(fields[2])
		if usesUnifiedMemory {
			unifiedMemory = true
		} else if mib, err := strconv.ParseUint(fields[2], 10, 64); err == nil {
			vramBytes = mib * 1024 * 1024
		}
		gpus = append(gpus, GPUInfo{
			Name:                  name,
			VramBytes:             vramBytes,
			statsKey:              uuid,
			usesSystemMemoryUsage: usesUnifiedMemory,
		})
	}
	return gpus, unifiedMemory
}

// splitCSVRow splits one nvidia-smi CSV row on commas and trims surrounding
// whitespace from each field (the tool emits ", " separators). Returns nil for
// a blank line so callers can skip it.
func splitCSVRow(line string) []string {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	parts := strings.Split(line, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}
