// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

//go:build !linux

package main

// freeBytes is unknown off Linux. The Qualcomm engine runs on Linux only.
func freeBytes(string) (uint64, bool) { return 0, false }
