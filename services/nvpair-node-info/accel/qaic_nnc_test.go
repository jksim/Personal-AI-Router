// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

package accel

import (
	"encoding/binary"
	"strings"
	"testing"
)

// statusResponseFixture builds a well-formed 512-byte STATUS_RESP transaction.
// Offsets come from Qualcomm's QNncProtocol.h / QAicHostApiInfo.h, verified by
// compiling the real headers and printing offsetof.
func statusResponseFixture(mutate func(b []byte)) []byte {
	b := make([]byte, nncStatusResponseLen)
	binary.LittleEndian.PutUint32(b[0:], nncTransactionPassthroughKU)
	binary.LittleEndian.PutUint32(b[4:], nncStatusResponseLen)
	binary.LittleEndian.PutUint32(b[8:], nncCommandStatusResp)
	binary.LittleEndian.PutUint32(b[12:], 0) // status_code: success
	b[offInfoFormatVersion] = deviceInfoFormatVersion
	copy(b[offBoardSerial:], "ULTRA-0123456789")
	binary.LittleEndian.PutUint32(b[offDramTotalMB:], 131072) // 128 GiB in MB
	binary.LittleEndian.PutUint32(b[offDramFreeMB:], 98304)
	b[offNspTotal] = 16
	b[offNspFree] = 4
	b[offSkuType] = skuPCIeUltra
	if mutate != nil {
		mutate(b)
	}
	return b
}

// TestEncodeStatusRequest pins the request bytes exactly.
//
// The transaction header claims 24 bytes while only 16 are meaningful: the SDK
// sizes it as passthrough + request but writes the request over the command
// field at offset 8. Firmware tolerates the trailing zeros, and matching the
// SDK byte for byte is the safest thing to send to a device we cannot test
// against yet.
func TestEncodeStatusRequest(t *testing.T) {
	request := encodeStatusRequest()

	if len(request) != nncStatusRequestLen {
		t.Fatalf("request length = %d, want %d", len(request), nncStatusRequestLen)
	}
	want := []struct {
		offset int
		value  uint32
		name   string
	}{
		{0, nncTransactionPassthroughUK, "transaction type"},
		{4, nncStatusRequestLen, "transaction length"},
		{8, nncCommandStatusReq, "command type"},
		{12, 0, "info_type and padding"},
	}
	for _, w := range want {
		if got := binary.LittleEndian.Uint32(request[w.offset:]); got != w.value {
			t.Fatalf("%s at offset %d = %d, want %d", w.name, w.offset, got, w.value)
		}
	}
	if nncStatusRequestLen != 24 {
		t.Fatalf("request declares %d bytes, want the SDK's 24", nncStatusRequestLen)
	}
}

// TestDecodeStatusResponse reads the fields the accelerator layer needs out of
// a known-good response.
func TestDecodeStatusResponse(t *testing.T) {
	status, err := decodeStatusResponse(statusResponseFixture(nil))
	if err != nil {
		t.Fatalf("decodeStatusResponse() error = %v", err)
	}

	if status.BoardSerial != "ULTRA-0123456789" {
		t.Fatalf("board serial = %q", status.BoardSerial)
	}
	if status.DramTotalMB != 131072 || status.DramFreeMB != 98304 {
		t.Fatalf("dram = %d/%d MB", status.DramFreeMB, status.DramTotalMB)
	}
	if status.NspTotal != 16 || status.NspFree != 4 {
		t.Fatalf("nsp = %d/%d", status.NspFree, status.NspTotal)
	}
	if status.SkuName != "PCIe Ultra" {
		t.Fatalf("sku = %q, want PCIe Ultra", status.SkuName)
	}
}

// TestDecodeStatusResponseNspOccupancy pins the load signal itself. The
// Qualcomm stack exposes no utilization percentage anywhere, so occupancy is
// derived from NSP counts: each NSP runs one workload at a time.
func TestDecodeStatusResponseNspOccupancy(t *testing.T) {
	cases := []struct {
		name      string
		total     byte
		free      byte
		wantLoad  float64
		wantValid bool
	}{
		{name: "idle", total: 16, free: 16, wantLoad: 0, wantValid: true},
		{name: "quarter busy", total: 16, free: 12, wantLoad: 0.25, wantValid: true},
		{name: "saturated", total: 16, free: 0, wantLoad: 1, wantValid: true},
		// A device reporting no NSPs at all cannot produce a ratio. It must
		// report invalid rather than dividing by zero or claiming idle.
		{name: "no nsps reported", total: 0, free: 0, wantValid: false},
		// Free above total is nonsense; clamp rather than emit a negative.
		{name: "free exceeds total", total: 8, free: 12, wantLoad: 0, wantValid: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			status, err := decodeStatusResponse(statusResponseFixture(func(b []byte) {
				b[offNspTotal] = c.total
				b[offNspFree] = c.free
			}))
			if err != nil {
				t.Fatalf("decodeStatusResponse() error = %v", err)
			}
			load := status.Load()
			if load.Valid != c.wantValid {
				t.Fatalf("load.Valid = %v, want %v", load.Valid, c.wantValid)
			}
			if c.wantValid && load.Fraction != c.wantLoad {
				t.Fatalf("load = %v, want %v", load.Fraction, c.wantLoad)
			}
		})
	}
}

// TestDecodeStatusResponseBoardSerialNotTerminated covers a documented device
// behaviour: the serial field is a fixed 32 bytes and the device does not
// always NUL-terminate it. Reading past the field would splice the compiler
// version onto the end of the serial, and the serial is the key cards are
// grouped by.
func TestDecodeStatusResponseBoardSerialNotTerminated(t *testing.T) {
	full := strings.Repeat("A", 32)
	status, err := decodeStatusResponse(statusResponseFixture(func(b []byte) {
		copy(b[offBoardSerial:offBoardSerial+32], full)
		// Something recognisable immediately after the field.
		binary.LittleEndian.PutUint32(b[offBoardSerial+32:], 0xDEADBEEF)
	}))
	if err != nil {
		t.Fatalf("decodeStatusResponse() error = %v", err)
	}
	if status.BoardSerial != full {
		t.Fatalf("board serial = %q, want exactly 32 A's", status.BoardSerial)
	}
}

// TestDecodeStatusResponseRejectsMalformed refuses to publish numbers from a
// response we do not understand. Every rejection here would otherwise become a
// plausible-looking but wrong reading — a card's memory or occupancy invented
// from whatever bytes happened to be at those offsets.
func TestDecodeStatusResponseRejectsMalformed(t *testing.T) {
	cases := []struct {
		name    string
		payload []byte
		reason  string
	}{
		{
			name:    "truncated",
			payload: statusResponseFixture(nil)[:128],
			reason:  "short response",
		},
		{
			name:    "empty",
			payload: nil,
			reason:  "no response at all",
		},
		{
			name: "wrong command type",
			payload: statusResponseFixture(func(b []byte) {
				binary.LittleEndian.PutUint32(b[8:], nncCommandStatusReq)
			}),
			reason: "a request echoed back is not a response",
		},
		{
			name: "device reported a failure status",
			payload: statusResponseFixture(func(b []byte) {
				binary.LittleEndian.PutUint32(b[12:], ^uint32(0)) // -1
			}),
			reason: "negative status code",
		},
		{
			name: "unknown info format version",
			payload: statusResponseFixture(func(b []byte) {
				b[offInfoFormatVersion] = deviceInfoFormatVersion + 1
			}),
			reason: "firmware laid the struct out differently",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := decodeStatusResponse(c.payload); err == nil {
				t.Fatalf("decoded a malformed response (%s)", c.reason)
			}
		})
	}
}
