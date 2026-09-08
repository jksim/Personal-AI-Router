// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package accel

import (
	"fmt"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

// manageResponse is what the device wrote back over the request buffer.
type manageResponse struct {
	// Payload is the response bytes, truncated to the length the driver
	// reported. It aliases the transfer buffer, which is not reused.
	Payload []byte

	// Count is the number of transactions in the response.
	Count uint32

	// NACKed reports that the device rejected the request but still
	// returned a well-formed response describing why. The payload is worth
	// decoding; the request is not worth retrying unchanged.
	NACKed bool
}

// qaicManage opens an accelerator device node and performs one management
// transaction against it.
//
// The device nodes are root-owned and mode 0600 by default, so an unprivileged
// process gets a permission error here. That is reported as-is rather than
// treated as absent hardware: "the card is there and we may not read it" is a
// different operator problem from "there is no card", and only the first is
// fixed by a udev rule.
func qaicManage(devicePath string, request []byte, count uint32) (manageResponse, error) {
	file, err := os.OpenFile(devicePath, os.O_RDWR, 0)
	if err != nil {
		return manageResponse{}, fmt.Errorf("open %s: %w", devicePath, err)
	}
	defer file.Close()
	return qaicManageFD(file.Fd(), request, count)
}

// qaicManageFD issues DRM_IOCTL_QAIC_MANAGE on an already-open descriptor.
func qaicManageFD(fd uintptr, request []byte, count uint32) (manageResponse, error) {
	buf, msg := encodeManage(request, count)
	msg.Data = uint64(uintptr(unsafe.Pointer(&buf[0])))

	_, _, errno := unix.Syscall(
		unix.SYS_IOCTL,
		fd,
		uintptr(manageIoctl),
		uintptr(unsafe.Pointer(&msg)),
	)
	// msg.Data holds the buffer's address as a plain integer, which the
	// garbage collector cannot follow. Without this the collector is free to
	// move or reclaim buf while the driver is still writing the response
	// into it.
	runtime.KeepAlive(buf)

	nacked, err := classifyManageErrno(errno)
	if err != nil {
		return manageResponse{}, err
	}
	if int(msg.Len) > len(buf) {
		return manageResponse{}, fmt.Errorf(
			"qaic manage: driver reported a %d byte response into a %d byte buffer", msg.Len, len(buf))
	}
	return manageResponse{Payload: buf[:msg.Len], Count: msg.Count, NACKed: nacked}, nil
}

// classifyManageErrno separates "the device answered no" from "the call
// failed".
//
// ECANCELED is the driver's way of returning a device NACK: the ioctl reports
// failure, but a valid response has still been copied back and explains the
// rejection. Treating it as a plain error throws that away and leaves the
// caller guessing.
func classifyManageErrno(errno unix.Errno) (bool, error) {
	switch errno {
	case 0:
		return false, nil
	case unix.ECANCELED:
		return true, nil
	default:
		return false, fmt.Errorf("qaic manage ioctl: %w", errno)
	}
}
