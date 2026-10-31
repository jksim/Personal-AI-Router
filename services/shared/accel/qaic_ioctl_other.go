// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

//go:build !linux

package accel

import "errors"

// errQAICUnsupported keeps the package building on hosts with no qaic driver
// interface. Qualcomm's accelerators are Linux-only, so this is a compile
// concern rather than a runtime one.
var errQAICUnsupported = errors.New("qaic: management ioctl is only available on Linux")

type manageResponse struct {
	Payload []byte
	Count   uint32
	NACKed  bool
}

func qaicManage(string, []byte, uint32) (manageResponse, error) {
	return manageResponse{}, errQAICUnsupported
}
