// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// qpcTarball builds a small QPC archive in the catalog's layout.
func qpcTarball(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func serveQPC(t *testing.T, archive []byte) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(archive)
	}))
	t.Cleanup(server.Close)
	return server.URL + "/qpc.tar.gz"
}

func testQPCEntry(url string, archive []byte) qaicCatalogEntry {
	sum := sha256.Sum256(archive)
	return qaicCatalogEntry{
		Model: "Example/Tiny", QPC: "qpc_16cores_128pl_8192cl_4devices_mxfp6_mxint8", URL: url,
		SHA256: hex.EncodeToString(sum[:]), Size: uint64(len(archive)), UnpackedSize: 64,
		SDK: qaicCatalogSDK, Devices: 4, Batch: 1, Context: 8192, Prefill: 128,
	}
}

var tinyQPC = map[string]string{
	"qpc-abc/qconfig.json":         "{}",
	"qpc-abc/qpc/programqpc.bin":   "compiled",
	"qpc-abc/specializations.json": "{}",
}

func TestQaicPullUnpacksAVerifiedQPC(t *testing.T) {
	archive := qpcTarball(t, tinyQPC)
	entry := testQPCEntry(serveQPC(t, archive), archive)
	modelsDir := t.TempDir()
	e := gateExecutor(t, readyFour)

	if _, err := e.pullQPCEntry(context.Background(), "qaic", entry, modelsDir); err != nil {
		t.Fatalf("pullQPCEntry() error = %v", err)
	}
	dest := filepath.Join(modelsDir, "Example/Tiny", entry.QPC)
	if got, err := os.ReadFile(filepath.Join(dest, "qpc-abc/qpc/programqpc.bin")); err != nil || string(got) != "compiled" {
		t.Fatalf("programqpc.bin = %q, %v", got, err)
	}
	args, err := os.ReadFile(filepath.Join(dest, "launch.args"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "--device-group 0,1,2,3 --max-model-len 8192 --max-num-seqs 1 --max-seq-len-to-capture 128\n"; string(args) != want {
		t.Fatalf("launch.args = %q, want %q", args, want)
	}
	if leftovers, _ := os.ReadDir(filepath.Join(modelsDir, ".partial")); len(leftovers) != 0 {
		t.Fatalf(".partial still holds %v", leftovers)
	}
}

func TestQaicPullRefusesAMismatchedSHA256(t *testing.T) {
	archive := qpcTarball(t, tinyQPC)
	entry := testQPCEntry(serveQPC(t, archive), archive)
	entry.SHA256 = strings.Repeat("0", 64)
	modelsDir := t.TempDir()
	e := gateExecutor(t, readyFour)

	_, err := e.pullQPCEntry(context.Background(), "qaic", entry, modelsDir)
	if err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("pullQPCEntry() error = %v, want a SHA-256 mismatch", err)
	}
	if _, err := os.Stat(filepath.Join(modelsDir, "Example/Tiny")); !os.IsNotExist(err) {
		t.Fatalf("a QPC that failed its SHA-256 was left in the models directory (%v)", err)
	}
	if leftovers, _ := os.ReadDir(filepath.Join(modelsDir, ".partial")); len(leftovers) != 0 {
		t.Fatalf(".partial still holds %v", leftovers)
	}
}

func TestQaicPullRefusesAPathOutsideTheQPC(t *testing.T) {
	archive := qpcTarball(t, map[string]string{"../escape": "x"})
	entry := testQPCEntry(serveQPC(t, archive), archive)
	modelsDir := t.TempDir()
	e := gateExecutor(t, readyFour)

	if _, err := e.pullQPCEntry(context.Background(), "qaic", entry, modelsDir); err == nil {
		t.Fatal("an archive entry outside the QPC directory was accepted")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(modelsDir), "escape")); !os.IsNotExist(err) {
		t.Fatal("the archive wrote outside the models directory")
	}
}

func TestQaicPullRefusesAModelNotInTheCatalog(t *testing.T) {
	e := gateExecutor(t, readyFour)
	if _, err := e.pullQPC(context.Background(), "qaic", "Nobody/Unknown", t.TempDir()); err == nil || !strings.Contains(err.Error(), "catalog") {
		t.Fatalf("pullQPC() error = %v, want 'not in the QPC catalog'", err)
	}
}
