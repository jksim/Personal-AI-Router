// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

package accel

// The qaic driver's management ioctl, and the argument struct it takes.
//
// Encoding and layout live here, untagged, so they are pinned by tests on any
// host; only the syscall itself is platform-specific.

// manageIoctl is DRM_IOCTL_QAIC_MANAGE: a DRM command ioctl ('d', base 0x40)
// at QAIC's offset 0x00, read-write, carrying a 16-byte manageMsg.
const manageIoctl = 0xC0106440

// maxManageMsgLen is the driver's QAIC_MANAGE_MAX_MSG_LENGTH.
//
// Every request buffer is allocated at this size regardless of how small the
// request is, because the driver copies the response back over the same
// userspace buffer and updates the length in place. A buffer sized to the
// request would be overwritten by a larger reply.
const maxManageMsgLen = 4096

// manageMsg mirrors the kernel's struct qaic_manage_msg:
//
//	struct qaic_manage_msg { __u32 len; __u32 count; __u64 data; };
//
// Data is a userspace address held as a plain integer, which means the garbage
// collector cannot see it. Every caller must keep the buffer alive across the
// syscall itself — see the platform implementation.
type manageMsg struct {
	Len   uint32
	Count uint32
	Data  uint64
}

// newManageBuffer allocates a transfer buffer of the only correct size.
func newManageBuffer() []byte {
	return make([]byte, maxManageMsgLen)
}

// encodeManage copies request into a full-size transfer buffer and returns it
// with the matching descriptor. count is the number of transactions the
// request contains.
//
// It panics on a request that cannot fit: that is a programming error in a
// caller building a message, not a runtime condition to be handled, and
// silently truncating would send the device a malformed transaction.
func encodeManage(request []byte, count uint32) ([]byte, manageMsg) {
	if len(request) > maxManageMsgLen {
		panic("accel: qaic manage request exceeds the driver's maximum message length")
	}
	buf := newManageBuffer()
	copy(buf, request)
	return buf, manageMsg{Len: uint32(len(request)), Count: count}
}
