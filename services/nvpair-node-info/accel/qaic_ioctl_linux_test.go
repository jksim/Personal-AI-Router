// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package accel

import (
	"errors"
	"testing"

	"golang.org/x/sys/unix"
)

// TestClassifyManageErrno pins the one errno with non-obvious meaning.
// ECANCELED is a device NACK carrying a valid response, not a failed call; a
// caller that discards it loses the device's explanation of the rejection.
func TestClassifyManageErrno(t *testing.T) {
	cases := []struct {
		name       string
		errno      unix.Errno
		wantNACK   bool
		wantErrIs  error
		wantAnyErr bool
	}{
		{name: "success", errno: 0},
		{name: "device NACK carries a response", errno: unix.ECANCELED, wantNACK: true},
		{name: "permission denied is a real failure", errno: unix.EACCES, wantErrIs: unix.EACCES, wantAnyErr: true},
		{name: "no such device is a real failure", errno: unix.ENODEV, wantErrIs: unix.ENODEV, wantAnyErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			nacked, err := classifyManageErrno(c.errno)
			if nacked != c.wantNACK {
				t.Fatalf("nacked = %v, want %v", nacked, c.wantNACK)
			}
			if c.wantAnyErr {
				if err == nil {
					t.Fatal("expected an error")
				}
				if !errors.Is(err, c.wantErrIs) {
					t.Fatalf("error = %v, want it to wrap %v", err, c.wantErrIs)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// TestQAICManageMissingDeviceReportsOpenError keeps "cannot open the node"
// distinguishable from "the ioctl failed". A permission problem on
// /dev/accel/* is fixed by a udev rule, and the message has to say so.
func TestQAICManageMissingDeviceReportsOpenError(t *testing.T) {
	_, err := qaicManage("/dev/accel/definitely-not-here", []byte{0}, 1)
	if err == nil {
		t.Fatal("expected an error opening a nonexistent device")
	}
	if !errors.Is(err, unix.ENOENT) {
		t.Fatalf("error = %v, want it to wrap ENOENT", err)
	}
}
