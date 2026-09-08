// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

package accel

import (
	"testing"
	"unsafe"
)

// iowr recomputes the ioctl request number from the kernel's own encoding
// rather than restating the constant. A test that hardcoded 0xC0106440 on both
// sides would pass even if the encoding were wrong; deriving it here means the
// two have to agree for a reason.
//
//	dir(2) | size(14) | type(8) | nr(8), with dir = READ|WRITE = 3
func iowr(typ, nr, size uint32) uint32 {
	const dirReadWrite = 3
	return dirReadWrite<<30 | size<<16 | typ<<8 | nr
}

// TestManageIoctlNumber pins DRM_IOCTL_QAIC_MANAGE. DRM command ioctls are
// 'd' (0x64) based at DRM_COMMAND_BASE 0x40, and QAIC's MANAGE is offset 0x00.
// The size in the encoding is the argument struct's, not the payload's.
func TestManageIoctlNumber(t *testing.T) {
	const (
		drmIoctlType    = 0x64 // 'd'
		drmCommandBase  = 0x40
		qaicManageIndex = 0x00
	)
	want := iowr(drmIoctlType, drmCommandBase+qaicManageIndex, uint32(unsafe.Sizeof(manageMsg{})))

	if manageIoctl != want {
		t.Fatalf("manageIoctl = %#x, want %#x", manageIoctl, want)
	}
	// The value the driver's headers imply, stated once so a change is loud.
	if manageIoctl != 0xC0106440 {
		t.Fatalf("manageIoctl = %#x, want 0xC0106440", manageIoctl)
	}
}

// TestManageMsgLayout pins the ioctl argument struct against the kernel's
// struct qaic_manage_msg { __u32 len; __u32 count; __u64 data; }. Go could
// legally pad this differently; if it ever does, the driver reads garbage.
func TestManageMsgLayout(t *testing.T) {
	if got := unsafe.Sizeof(manageMsg{}); got != 16 {
		t.Fatalf("sizeof(manageMsg) = %d, want 16", got)
	}
	var msg manageMsg
	offsets := []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"Len", unsafe.Offsetof(msg.Len), 0},
		{"Count", unsafe.Offsetof(msg.Count), 4},
		{"Data", unsafe.Offsetof(msg.Data), 8},
	}
	for _, o := range offsets {
		if o.got != o.want {
			t.Fatalf("offsetof(%s) = %d, want %d", o.name, o.got, o.want)
		}
	}
}

// TestManageBufferIsAlwaysMaxLength is the subtle one. The driver copies the
// response back over the same userspace buffer the request came from and
// updates len in place, so the buffer must be big enough for the reply no
// matter how small the request is. A 24-byte request buffer would be a
// controlled heap overwrite waiting for a 512-byte status response.
func TestManageBufferIsAlwaysMaxLength(t *testing.T) {
	if maxManageMsgLen != 4096 {
		t.Fatalf("maxManageMsgLen = %d, want the driver's 4096", maxManageMsgLen)
	}

	buf := newManageBuffer()
	if len(buf) != maxManageMsgLen {
		t.Fatalf("buffer len = %d, want %d regardless of request size", len(buf), maxManageMsgLen)
	}

	// A request far smaller than the buffer must not shrink it.
	request := make([]byte, 24)
	buf, msg := encodeManage(request, 1)
	if len(buf) != maxManageMsgLen {
		t.Fatalf("buffer len = %d after a 24-byte request, want %d", len(buf), maxManageMsgLen)
	}
	if msg.Len != uint32(len(request)) {
		t.Fatalf("msg.Len = %d, want the request length %d", msg.Len, len(request))
	}
	if msg.Count != 1 {
		t.Fatalf("msg.Count = %d, want 1", msg.Count)
	}
	// The request must land at the front of the buffer the pointer refers to.
	for i := range request {
		if buf[i] != request[i] {
			t.Fatalf("request byte %d not copied into the buffer", i)
		}
	}
}

// TestEncodeManageRejectsOversizeRequest keeps a caller from silently
// truncating into the fixed buffer.
func TestEncodeManageRejectsOversizeRequest(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("encoding a request larger than the buffer did not panic")
		}
	}()
	encodeManage(make([]byte, maxManageMsgLen+1), 1)
}
