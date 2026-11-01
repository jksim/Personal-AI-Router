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
	"time"
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

// blockingQPC serves the first half of archive, then waits for release.
func blockingQPC(t *testing.T, archive []byte) (string, chan struct{}) {
	t.Helper()
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(archive[:len(archive)/2])
		w.(http.Flusher).Flush()
		select {
		case <-release:
			_, _ = w.Write(archive[len(archive)/2:])
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
		server.Close()
	})
	return server.URL + "/qpc.tar.gz", release
}

func TestQaicPullRefusesASecondPullOfTheSameModel(t *testing.T) {
	archive := qpcTarball(t, tinyQPC)
	url, release := blockingQPC(t, archive)
	entry := testQPCEntry(url, archive)
	modelsDir := t.TempDir()
	e := gateExecutor(t, readyFour)

	first := make(chan error, 1)
	go func() {
		_, err := e.pullQPCEntry(context.Background(), "qaic", entry, modelsDir)
		first <- err
	}()
	time.Sleep(200 * time.Millisecond)
	if _, err := e.pullQPCEntry(context.Background(), "qaic", entry, modelsDir); err == nil || !strings.Contains(err.Error(), "already") {
		t.Fatalf("second pull error = %v, want 'already being pulled'", err)
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatalf("first pull error = %v", err)
	}
}

func TestQaicPullCancelLeavesNothing(t *testing.T) {
	archive := qpcTarball(t, tinyQPC)
	url, _ := blockingQPC(t, archive)
	entry := testQPCEntry(url, archive)
	modelsDir := t.TempDir()
	e := gateExecutor(t, readyFour)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()
	if _, err := e.pullQPCEntry(ctx, "qaic", entry, modelsDir); err == nil {
		t.Fatal("a cancelled pull reported success")
	}
	if leftovers, _ := os.ReadDir(filepath.Join(modelsDir, ".partial")); len(leftovers) != 0 {
		t.Fatalf(".partial still holds %v", leftovers)
	}
}

func TestQaicPullHashesBytesAfterTheGzipEnd(t *testing.T) {
	archive := qpcTarball(t, tinyQPC)
	padded := append(append([]byte{}, archive...), []byte("trailing bytes")...)
	entry := testQPCEntry(serveQPC(t, padded), archive) // pinned SHA-256 of the archive alone
	entry.Size = uint64(len(padded))
	e := gateExecutor(t, readyFour)

	if _, err := e.pullQPCEntry(context.Background(), "qaic", entry, t.TempDir()); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("pull error = %v, want a SHA-256 mismatch that covers the trailing bytes", err)
	}
}

func TestQaicPullRefusesMoreThanTheCatalogSize(t *testing.T) {
	archive := qpcTarball(t, tinyQPC)
	entry := testQPCEntry(serveQPC(t, archive), archive)
	entry.Size = uint64(len(archive) / 2)
	e := gateExecutor(t, readyFour)

	if _, err := e.pullQPCEntry(context.Background(), "qaic", entry, t.TempDir()); err == nil || !strings.Contains(err.Error(), "larger") {
		t.Fatalf("pull error = %v, want a refusal of a download larger than the catalog size", err)
	}
}

func TestQaicPullRefusesWithoutFreeSpace(t *testing.T) {
	archive := qpcTarball(t, tinyQPC)
	entry := testQPCEntry(serveQPC(t, archive), archive)
	entry.UnpackedSize = 1 << 62
	e := gateExecutor(t, readyFour)

	if _, err := e.pullQPCEntry(context.Background(), "qaic", entry, t.TempDir()); err == nil || !strings.Contains(err.Error(), "free") {
		t.Fatalf("pull error = %v, want a free-space refusal", err)
	}
}

func TestQaicPullRemovesLeftoverPartials(t *testing.T) {
	archive := qpcTarball(t, tinyQPC)
	entry := testQPCEntry(serveQPC(t, archive), archive)
	modelsDir := t.TempDir()
	old := filepath.Join(modelsDir, ".partial", "Other__Model__qpc-crashed")
	if err := os.MkdirAll(old, 0o755); err != nil {
		t.Fatal(err)
	}
	e := gateExecutor(t, readyFour)

	if _, err := e.pullQPCEntry(context.Background(), "qaic", entry, modelsDir); err != nil {
		t.Fatalf("pull error = %v", err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("a leftover partial directory from a crash is still there (%v)", err)
	}
}

func TestQaicPullDoesNotNeedDocker(t *testing.T) {
	archive := qpcTarball(t, tinyQPC)
	entry := testQPCEntry(serveQPC(t, archive), archive)
	e := gateExecutor(t, func() (qaicHostReady, []qaicHostProblem) {
		ready, _ := readyFour()
		ready.DockerUsable = false
		return ready, []qaicHostProblem{{Message: "Docker is not installed.", Fix: "Install Docker.", Blocking: true, Check: qaicCheckDocker}}
	})

	if _, err := e.pullQPCEntry(context.Background(), "qaic", entry, t.TempDir()); err != nil {
		t.Fatalf("pull error = %v, want a pull that does not need Docker", err)
	}
}

func TestQaicRemotePullFindsTheModelInParams(t *testing.T) {
	e := gateExecutor(t, readyFour)
	_, err := e.PullModelStream(context.Background(), "qaic", "", []byte(`{"model":"Nobody/Unknown"}`))
	if err == nil || !strings.Contains(err.Error(), "Nobody/Unknown") {
		t.Fatalf("PullModelStream error = %v, want the model from params", err)
	}
}
