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
	binary.LittleEndian.PutUint32(b[0:], qaicTransPassthroughFromDev)
	binary.LittleEndian.PutUint32(b[4:], nncStatusResponseLen)
	binary.LittleEndian.PutUint32(b[8:], nncCommandStatusResp)
	binary.LittleEndian.PutUint32(b[12:], 0) // status_code: success
	b[offInfoFormatVersion] = deviceInfoFormatVersion
	copy(b[offBoardSerial:], "ULTRA-0123456789")
	// Real per-SoC figures from an Ultra: KB, not MB as the header claims.
	binary.LittleEndian.PutUint32(b[offDramTotalKB:], 31391744)
	binary.LittleEndian.PutUint32(b[offDramFreeKB:], 31138895)
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
	if status.DramTotalKB != 31391744 || status.DramFreeKB != 31138895 {
		t.Fatalf("dram = %d/%d KB", status.DramFreeKB, status.DramTotalKB)
	}
	if status.NspTotal != 16 || status.NspFree != 4 {
		t.Fatalf("nsp = %d/%d", status.NspFree, status.NspTotal)
	}
	if status.SkuName != "PCIe Ultra" {
		t.Fatalf("sku = %q, want PCIe Ultra", status.SkuName)
	}
}

// TestDecodeStatusResponseNspCounts pins the decoded NSP counts. They are
// reported as decoded; they are not turned into a load.
func TestDecodeStatusResponseNspCounts(t *testing.T) {
	status, err := decodeStatusResponse(statusResponseFixture(func(b []byte) {
		b[offNspTotal] = 16
		b[offNspFree] = 0
	}))
	if err != nil {
		t.Fatalf("decodeStatusResponse() error = %v", err)
	}
	if status.NspTotal != 16 || status.NspFree != 0 {
		t.Fatalf("nsp = %d/%d, want 0 free of 16", status.NspFree, status.NspTotal)
	}
}

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
