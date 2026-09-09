// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

package accel

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strings"
)

// NNC status query: the one transaction that yields everything this package
// needs from a Qualcomm accelerator — board serial, memory, and NSP occupancy.
//
// Layout comes from Qualcomm's QNncProtocol.h and QAicHostApiInfo.h (public,
// BSD-3-Clause-Clear, in quic/software-kit-for-qualcomm-cloud-ai-100). Offsets
// were verified by compiling those headers and printing offsetof rather than
// by counting field widths, because one field is declared with the wrong size
// macro and hand-arithmetic gets it wrong.
//
// Everything is little-endian and packed.

const (
	// nncTransactionPassthroughUK is what we send. NNC's own enum defines
	// only the user/kernel directions (UK = 1, KU = 2).
	nncTransactionPassthroughUK = 1

	// qaicTransPassthroughFromDev is what comes back, and it is the KERNEL's
	// constant, not NNC's: the driver stamps QAIC_TRANS_PASSTHROUGH_FROM_DEV
	// from its own richer enum in the UAPI header. Verified against real
	// hardware, which returns 3 where NNC's KU would have been 2.
	qaicTransPassthroughFromDev = 3

	nncCommandStatusReq  = 13
	nncCommandStatusResp = 14

	// deviceInfoFormatVersion is the layout these offsets describe. The
	// device reports its own; a mismatch means different firmware laid the
	// struct out differently and none of the offsets below can be trusted.
	deviceInfoFormatVersion = 12

	// nncStatusRequestLen is what the SDK puts in the transaction header.
	//
	// Only 16 bytes are meaningful: the request struct is written over the
	// command field at offset 8, so the last 8 bytes are zeros the firmware
	// ignores. The SDK sizes the header as passthrough + request anyway. We
	// send the same thing, because bug-compatibility with the only
	// implementation known to work beats being tidy against hardware we
	// cannot yet test.
	nncStatusRequestLen = 24

	// nncStatusResponseLen is the full response transaction.
	nncStatusResponseLen = 512
)

// Field offsets from the first byte of the response transaction.
const (
	offInfoFormatVersion = 16
	offBoardSerial       = 140
	boardSerialLen       = 32
	offDramTotalKB       = 176
	offDramFreeKB        = 180
	offNspTotal          = 190
	offNspFree           = 191
	offSkuType           = 340
)

// SKU ids from nnc_transaction_sku_query_type_t.
const skuPCIeUltra = 5

var skuNames = map[byte]string{
	0:  "Invalid",
	1:  "M.2",
	2:  "PCIe",
	3:  "PCIe Pro",
	4:  "PCIe Lite",
	5:  "PCIe Ultra",
	6:  "Auto",
	7:  "PCIe Ultra Plus",
	8:  "PCIe 080",
	9:  "PCIe Ultra 080",
	10: "AI200",
}

// qaicStatus is one SoC's reported state.
type qaicStatus struct {
	// BoardSerial identifies the physical card. Several SoCs on one card
	// report the same serial, which is how they are grouped back together.
	BoardSerial string

	// DramTotalKB and DramFreeKB are on-device memory in KILOBYTES, despite
	// QAicHostApiInfo.h commenting both fields as MB. Real hardware settles
	// it: one Ultra SoC reports 31391744, which is 29.9 GiB as KB and an
	// impossible 30 TB as MB, and four of them sum to the card's 128 GB.
	DramTotalKB uint32
	DramFreeKB  uint32

	// NspTotal and NspFree are neural signal processor counts. Each NSP runs
	// one workload at a time, which makes their ratio a real occupancy
	// measure rather than a sampled percentage.
	NspTotal uint8
	NspFree  uint8

	SkuType uint8
	SkuName string
}

// Load derives occupancy from NSP counts.
//
// The Qualcomm stack exposes no utilization percentage anywhere — not in
// qaic-util, not in its Prometheus exporter — so nothing here is a reading of
// one. A device reporting no NSPs cannot produce a ratio and reports invalid
// rather than claiming to be idle, because "unknown" and "idle" send work to
// very different places.
func (s qaicStatus) Load() Load {
	if s.NspTotal == 0 {
		return Load{}
	}
	free := s.NspFree
	if free > s.NspTotal {
		// Nonsense from the device; treat as fully free rather than
		// producing a negative occupancy.
		free = s.NspTotal
	}
	busy := float64(s.NspTotal-free) / float64(s.NspTotal)
	return Load{Fraction: busy, Valid: true}
}

// encodeStatusRequest builds the STATUS_REQ transaction.
func encodeStatusRequest() []byte {
	request := make([]byte, nncStatusRequestLen)
	binary.LittleEndian.PutUint32(request[0:], nncTransactionPassthroughUK)
	binary.LittleEndian.PutUint32(request[4:], nncStatusRequestLen)
	binary.LittleEndian.PutUint32(request[8:], nncCommandStatusReq)
	// Offset 12 stays zero: info_type is never set by the SDK, and the
	// device answers with the full combined info block regardless.
	return request
}

// decodeStatusResponse reads a STATUS_RESP transaction.
//
// It refuses anything it does not fully recognise. Every field here feeds a
// scheduling or capacity decision, and a plausible-looking wrong number is
// worse than no number: an invented occupancy sends work to a card that cannot
// take it, and an invented serial merges two cards into one.
func decodeStatusResponse(payload []byte) (qaicStatus, error) {
	if len(payload) < nncStatusResponseLen {
		return qaicStatus{}, fmt.Errorf(
			"qaic status: response is %d bytes, want at least %d", len(payload), nncStatusResponseLen)
	}
	if got := binary.LittleEndian.Uint32(payload[0:]); got != qaicTransPassthroughFromDev {
		return qaicStatus{}, fmt.Errorf("qaic status: transaction type %d, want %d", got, qaicTransPassthroughFromDev)
	}
	if got := binary.LittleEndian.Uint32(payload[8:]); got != nncCommandStatusResp {
		return qaicStatus{}, fmt.Errorf("qaic status: command type %d, want %d", got, nncCommandStatusResp)
	}
	if code := int32(binary.LittleEndian.Uint32(payload[12:])); code != 0 {
		return qaicStatus{}, fmt.Errorf("qaic status: device reported status %d", code)
	}
	if got := payload[offInfoFormatVersion]; got != deviceInfoFormatVersion {
		return qaicStatus{}, fmt.Errorf(
			"qaic status: device info format version %d, want %d", got, deviceInfoFormatVersion)
	}

	sku := payload[offSkuType]
	name, ok := skuNames[sku]
	if !ok {
		name = fmt.Sprintf("Unknown SKU %d", sku)
	}

	return qaicStatus{
		BoardSerial: decodeFixedString(payload[offBoardSerial : offBoardSerial+boardSerialLen]),
		DramTotalKB: binary.LittleEndian.Uint32(payload[offDramTotalKB:]),
		DramFreeKB:  binary.LittleEndian.Uint32(payload[offDramFreeKB:]),
		NspTotal:    payload[offNspTotal],
		NspFree:     payload[offNspFree],
		SkuType:     sku,
		SkuName:     name,
	}, nil
}

// decodeFixedString reads a fixed-width device string. The device does not
// always NUL-terminate a field it fills completely, so the width is the bound —
// reading to the next NUL would splice the following field onto the end.
func decodeFixedString(field []byte) string {
	if end := bytes.IndexByte(field, 0); end >= 0 {
		field = field[:end]
	}
	return strings.TrimSpace(string(field))
}
