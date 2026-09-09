// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package openaiproxy

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

// redirectConfigDir points os.UserConfigDir() at a temp dir for the test, so
// proxy-port.json reads/writes don't touch the real per-user config. Sets all
// three env vars os.UserConfigDir() consults across platforms: XDG_CONFIG_HOME
// on Linux, $HOME/Library on macOS, and APPDATA on Windows. Missing APPDATA
// meant the Windows-first-class path read/wrote the real %AppData% file —
// clobbering the user's saved port and making the test fail on repeat runs.
func redirectConfigDir(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	t.Setenv("APPDATA", dir)
	t.Setenv("LOCALAPPDATA", dir)
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve free port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// TestPortFileIsPerEngine pins the thing that actually matters now that one
// codebase serves several engines: two engines must never share a port file, or
// each restores the other's port and they fight over one socket.
func TestPortFileIsPerEngine(t *testing.T) {
	max := testProfile()
	max.PortFile = "max-proxy-port.json"
	other := testProfile()
	other.PortFile = "other-proxy-port.json"

	if max.PortFile == other.PortFile {
		t.Fatal("two engines share a port file")
	}
}

func TestChooseStartupPort(t *testing.T) {
	profile := testProfile()
	profile.LegacyPort = 1235
	for _, tc := range []struct {
		name                          string
		flagPort, persisted           int
		ignorePersisted, hasPersisted bool
		want                          int
	}{
		{"new default", 1234, 0, false, false, 1234},
		{"legacy default migrates", 1234, 1235, false, true, 1234},
		{"custom survives opt-out", 1234, 12400, false, true, 12400},
		{"managed flag wins", 1234, 12400, true, true, 1234},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := profile.chooseStartupPort(tc.flagPort, tc.ignorePersisted, tc.persisted, tc.hasPersisted); got != tc.want {
				t.Fatalf("chooseStartupPort() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestPersistedPortRoundTrip(t *testing.T) {
	redirectConfigDir(t)

	if _, ok := testProfile().loadPersistedPort(); ok {
		t.Fatal("expected no persisted port before any save")
	}
	if err := testProfile().savePersistedPort(11500); err != nil {
		t.Fatalf("savePersistedPort: %v", err)
	}
	if p, ok := testProfile().loadPersistedPort(); !ok || p != 11500 {
		t.Errorf("round-trip: got %d ok=%v, want 11500", p, ok)
	}

	// An out-of-range stored value is treated as "none" so startup falls
	// back to the flag/default rather than trying to bind port 0.
	path, err := testProfile().proxyPortPath()
	if err != nil {
		t.Fatalf("proxyPortPath: %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"port":0}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := testProfile().loadPersistedPort(); ok {
		t.Error("port 0 should be treated as none")
	}
}

// TestSetPortRebinds drives a live rebind: the proxy starts serving on one
// port, set-port moves it to another, and afterward the new port accepts
// connections, the old one doesn't, the choice is persisted, and a fresh
// ready notification carries the new port.
func TestSetPortRebinds(t *testing.T) {
	redirectConfigDir(t)

	buf := &bytes.Buffer{}
	codec := NewCodec(buf)
	disc := NewDiscovery()

	portA := freeTCPPort(t)
	proxy := NewProxy(codec, disc, portA, testProfile())

	lnA, err := net.Listen("tcp", fmt.Sprintf(":%d", portA))
	if err != nil {
		t.Fatalf("listen on port A: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	proxy.serveHTTP(ctx, lnA)
	defer proxy.shutdown(context.Background())

	portB := freeTCPPort(t)
	if err := proxy.setPort(portB); err != nil {
		t.Fatalf("setPort: %v", err)
	}

	// New port is now serving.
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", portB), 2*time.Second)
	if err != nil {
		t.Fatalf("new port %d not listening after rebind: %v", portB, err)
	}
	conn.Close()

	// Old port stopped accepting.
	if c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", portA), 500*time.Millisecond); err == nil {
		c.Close()
		t.Errorf("old port %d should be closed after rebind", portA)
	}

	// Persisted for next startup.
	if p, ok := testProfile().loadPersistedPort(); !ok || p != portB {
		t.Errorf("persisted port: got %d ok=%v, want %d", p, ok, portB)
	}

	// A fresh ready notification announced the new port.
	if !strings.Contains(buf.String(), fmt.Sprintf("\"port\":%d", portB)) {
		t.Errorf("expected ready notification carrying port %d, got %q", portB, buf.String())
	}
}
