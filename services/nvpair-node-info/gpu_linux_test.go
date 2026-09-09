// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package main

import (
	"context"
	"errors"
	"os/exec"
	"reflect"
	"testing"
	"time"

	"nvpair-node-info/accel"
)

// TestNvidiaSmiCSVBuildsQueryArgs pins the exact argv the CSV helper hands the
// runner. The flags are a wire contract with nvidia-smi: dropping noheader or
// nounits changes every downstream parse, and neither parser validates its
// input shape, so a regression here surfaces as silently empty GPU data rather
// than an error.
func TestNvidiaSmiCSVBuildsQueryArgs(t *testing.T) {
	var got []string
	_, err := nvidiaSmiCSVWithRunner(
		context.Background(),
		time.Second,
		func(_ context.Context, args ...string) ([]byte, error) {
			got = args
			return nil, nil
		},
		"uuid,name,memory.total",
	)
	if err != nil {
		t.Fatalf("nvidiaSmiCSVWithRunner() error = %v", err)
	}
	want := []string{
		"--query-gpu=uuid,name,memory.total",
		"--format=csv,noheader,nounits",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("runner args = %q, want %q", got, want)
	}
}

// TestNvidiaSmiCSVReturnsStdout covers the success path plus the two output
// shapes the parsers must tolerate: empty stdout (driver present, no GPUs) and
// a trailing blank line (nvidia-smi always emits one). The helper is a pass
// through — it must not trim, so that parseNvidiaStatic keeps owning that.
func TestNvidiaSmiCSVReturnsStdout(t *testing.T) {
	cases := []struct {
		name string
		out  string
	}{
		{name: "single row", out: "GPU-aaa, NVIDIA RTX A4500, 20470\n"},
		{name: "empty stdout", out: ""},
		{name: "trailing blank line", out: "GPU-aaa, NVIDIA RTX A4500, 20470\n\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := nvidiaSmiCSVWithRunner(
				context.Background(),
				time.Second,
				func(_ context.Context, _ ...string) ([]byte, error) {
					return []byte(c.out), nil
				},
				"uuid,name,memory.total",
			)
			if err != nil {
				t.Fatalf("nvidiaSmiCSVWithRunner() error = %v", err)
			}
			if got != c.out {
				t.Fatalf("stdout = %q, want %q", got, c.out)
			}
		})
	}
}

// TestNvidiaSmiCSVMissingBinary pins the degrade path taken on every host with
// no NVIDIA driver: detectGPUs falls through to ghw and the stats collector
// latches. The helper must surface the error unwrapped, because decodeGPU logs
// it verbatim, and must still return whatever bytes arrived rather than
// discarding them.
func TestNvidiaSmiCSVMissingBinary(t *testing.T) {
	sentinel := exec.ErrNotFound
	got, err := nvidiaSmiCSVWithRunner(
		context.Background(),
		time.Second,
		func(_ context.Context, _ ...string) ([]byte, error) {
			return []byte("partial"), sentinel
		},
		"uuid",
	)
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want %v", err, sentinel)
	}
	if got != "partial" {
		t.Fatalf("stdout = %q, want the bytes the runner produced", got)
	}
}

// TestNvidiaSmiCSVTimeout proves the ceiling is real. A wedged driver can hang
// nvidia-smi indefinitely, which would stall both the one-shot startup detect
// and every stats tick; the runner must be handed a context that expires.
func TestNvidiaSmiCSVTimeout(t *testing.T) {
	_, err := nvidiaSmiCSVWithRunner(
		context.Background(),
		time.Millisecond,
		func(ctx context.Context, _ ...string) ([]byte, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
		"uuid",
	)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context deadline exceeded", err)
	}
}

// TestNvidiaSmiCSVHonorsParentCancellation pins that the parent context is
// respected as well as the timeout. detectGPUs and the collector both run
// under a process that can be shut down mid-query; a cancelled parent must
// abort the call rather than wait out the full nvidiaSmiTimeout.
func TestNvidiaSmiCSVHonorsParentCancellation(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := nvidiaSmiCSVWithRunner(
		parent,
		time.Minute,
		func(ctx context.Context, _ ...string) ([]byte, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
		"uuid",
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
	}
}

// TestHasKind pins the predicate behind the display-adapter fallback.
//
// The fallback must trigger when no GPU was identified even though other
// hardware was — a host with a working accelerator and a graphics card whose
// driver is broken. Gating on "nothing found at all" let the accelerator hide
// the GPU, which is exactly what happened on this host when a kernel upgrade
// outran the NVIDIA module: the card was on the bus and vanished from the API.
func TestHasKind(t *testing.T) {
	cases := []struct {
		name string
		gpus []GPUInfo
		want bool
	}{
		{name: "nothing found", gpus: nil, want: false},
		{
			name: "accelerator only still needs the display fallback",
			gpus: []GPUInfo{{Name: "Cloud AI", Kind: "accelerator"}},
			want: false,
		},
		{
			name: "a gpu was identified",
			gpus: []GPUInfo{{Name: "NVIDIA RTX A4500", Kind: "gpu"}},
			want: true,
		},
		{
			name: "accelerator alongside a gpu",
			gpus: []GPUInfo{
				{Name: "Cloud AI", Kind: "accelerator"},
				{Name: "NVIDIA RTX A4500", Kind: "gpu"},
			},
			want: true,
		},
		{
			// A previous ghw result must not satisfy the predicate, or a
			// second call would skip the fallback it just populated.
			name: "a display adapter does not count as a gpu",
			gpus: []GPUInfo{{Name: "Some adapter", Kind: "display"}},
			want: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hasKind(c.gpus, accel.KindGPU); got != c.want {
				t.Fatalf("hasKind(gpu) = %v, want %v", got, c.want)
			}
		})
	}
}
