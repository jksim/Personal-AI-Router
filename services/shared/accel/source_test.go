// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

package accel

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type fakeSource struct {
	name    string
	devices []Device
	err     error
	calls   *[]string
}

func (f fakeSource) Name() string { return f.name }

func (f fakeSource) Detect(context.Context) ([]Device, error) {
	if f.calls != nil {
		*f.calls = append(*f.calls, f.name)
	}
	return f.devices, f.err
}

// TestDetectPreservesSourceOrder pins that devices come back grouped in the
// order sources were declared. Ordering is a product decision, not an
// accident: the first device in the list drives the node card's primary ring
// in the desktop, so a host with a display adapter and an accelerator must not
// have them shuffle between polls.
func TestDetectPreservesSourceOrder(t *testing.T) {
	var calls []string
	first := fakeSource{name: "first", calls: &calls, devices: []Device{{Name: "A"}, {Name: "B"}}}
	second := fakeSource{name: "second", calls: &calls, devices: []Device{{Name: "C"}}}

	devices, errs := Detect(context.Background(), []Source{first, second})

	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	want := []Device{{Name: "A"}, {Name: "B"}, {Name: "C"}}
	if !reflect.DeepEqual(devices, want) {
		t.Fatalf("devices = %+v, want %+v", devices, want)
	}
	if !reflect.DeepEqual(calls, []string{"first", "second"}) {
		t.Fatalf("sources consulted in order %v", calls)
	}
}

// TestDetectEmptySourceIsNotAnError covers the ordinary case on almost every
// host: a source whose hardware simply is not present. It must report no
// devices and no error, so the caller does not log a failure for a machine
// that is behaving perfectly normally.
func TestDetectEmptySourceIsNotAnError(t *testing.T) {
	devices, errs := Detect(context.Background(), []Source{
		fakeSource{name: "absent", devices: nil, err: nil},
	})

	if len(errs) != 0 {
		t.Fatalf("an absent source reported an error: %v", errs)
	}
	if len(devices) != 0 {
		t.Fatalf("an absent source contributed %d devices", len(devices))
	}
}

// TestDetectFailingSourceDoesNotAbortChain is the property the whole chain
// exists for. The collector previously gave up on GPU data entirely when
// nvidia-smi failed; a host with a broken NVIDIA driver and a working
// accelerator must still report the accelerator.
func TestDetectFailingSourceDoesNotAbortChain(t *testing.T) {
	boom := errors.New("driver wedged")
	devices, errs := Detect(context.Background(), []Source{
		fakeSource{name: "broken", err: boom},
		fakeSource{name: "healthy", devices: []Device{{Name: "works"}}},
	})

	if len(devices) != 1 || devices[0].Name != "works" {
		t.Fatalf("healthy source lost its devices: %+v", devices)
	}
	if len(errs) != 1 || !errors.Is(errs[0], boom) {
		t.Fatalf("errs = %v, want the broken source's error", errs)
	}
}

// TestDetectReportsErrorPerSource keeps failures attributable. A caller
// logging these needs to name which tooling failed, because "GPU detection
// failed" on a mixed-vendor host tells an operator nothing.
func TestDetectReportsErrorPerSource(t *testing.T) {
	first := errors.New("first failed")
	second := errors.New("second failed")

	_, errs := Detect(context.Background(), []Source{
		fakeSource{name: "first", err: first},
		fakeSource{name: "second", err: second},
	})

	if len(errs) != 2 {
		t.Fatalf("got %d errors, want 2", len(errs))
	}
	for i, want := range []error{first, second} {
		if !errors.Is(errs[i], want) {
			t.Fatalf("errs[%d] = %v, want %v", i, errs[i], want)
		}
	}
	// The source name must survive into the message so the log names the tool.
	if got := errs[0].Error(); got == first.Error() {
		t.Fatalf("error was not annotated with its source: %q", got)
	}
}

// TestDetectNoSources guards the degenerate case rather than leaving it to
// chance: a build with no sources compiled in returns empty, not nil-panics.
func TestDetectNoSources(t *testing.T) {
	devices, errs := Detect(context.Background(), nil)
	if len(devices) != 0 || len(errs) != 0 {
		t.Fatalf("devices = %+v, errs = %v", devices, errs)
	}
}
