// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// The Qualcomm engine's pull runs here rather than as a manifest command: the
// download must stream through SHA-256 and tar at once, because a QPC is tens
// of gigabytes and there may be no room for both the archive and its contents.
// It unpacks into <models_dir>/.partial, which neither the launcher nor
// list_models reads, and moves the QPC into place only after its SHA-256 matches.

// pullQPC pulls a catalog QPC by model id.
func (e *Executor) pullQPC(ctx context.Context, engine, model, modelsDir string) (json.RawMessage, error) {
	entries, err := qaicCatalogEntries()
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.Model == model {
			return e.pullQPCEntry(ctx, engine, entry, modelsDir)
		}
	}
	return nil, fmt.Errorf("%s is not in the QPC catalog", model)
}

func (e *Executor) pullQPCEntry(ctx context.Context, engine string, entry qaicCatalogEntry, modelsDir string) (json.RawMessage, error) {
	if err := e.qaicGate(engine, entry.Model); err != nil {
		return nil, err
	}
	done, _ := json.Marshal(map[string]string{"status": "success", "model": entry.Model})
	dest := filepath.Join(modelsDir, filepath.FromSlash(entry.Model), entry.QPC)
	if fileExists(filepath.Join(dest, "launch.args")) {
		return done, nil
	}
	if err := validateDownloadURL(entry.URL); err != nil {
		return nil, err
	}

	partial := filepath.Join(modelsDir, ".partial", strings.ReplaceAll(entry.Model, "/", "__")+"__"+entry.QPC)
	if err := os.RemoveAll(partial); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(partial, 0o755); err != nil {
		return nil, err
	}
	kept := false
	defer func() {
		if !kept {
			_ = os.RemoveAll(partial)
		}
	}()

	pullCtx, watchdog := newPullProgressWatchdog(ctx, e.pullProgressTimeout)
	defer watchdog.stop()
	req, err := http.NewRequestWithContext(pullCtx, http.MethodGet, entry.URL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("pull %s: %w", entry.Model, watchdog.resolveError(pullCtx, err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("pull %s: the catalog returned HTTP %d", entry.Model, resp.StatusCode)
	}

	hash := sha256.New()
	counted := &qpcProgress{e: e, engine: engine, model: entry.Model, total: entry.Size, watchdog: watchdog, lastPercent: -1}
	body := io.TeeReader(io.TeeReader(resp.Body, counted), hash)
	if err := extractQPC(body, partial); err != nil {
		return nil, fmt.Errorf("pull %s: %w", entry.Model, watchdog.resolveError(pullCtx, err))
	}
	// gzip stops at its trailer; hash whatever follows too.
	if _, err := io.Copy(io.Discard, body); err != nil {
		return nil, fmt.Errorf("pull %s: %w", entry.Model, watchdog.resolveError(pullCtx, err))
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != entry.SHA256 {
		return nil, fmt.Errorf("pull %s: SHA-256 is %s, want %s; the file was refused", entry.Model, got, entry.SHA256)
	}

	if err := os.WriteFile(filepath.Join(partial, "launch.args"), []byte(qpcLaunchArgs(entry)), 0o644); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return nil, err
	}
	if err := os.RemoveAll(dest); err != nil {
		return nil, err
	}
	if err := os.Rename(partial, dest); err != nil {
		return nil, err
	}
	kept = true
	e.emitPullProgress(ProgressEvent{Engine: engine, Op: "pull", Stage: "success", Percent: 100, Message: entry.Model})
	return done, nil
}

// qpcLaunchArgs are the vLLM flags a QPC was compiled for. The launcher passes
// them unchanged.
func qpcLaunchArgs(entry qaicCatalogEntry) string {
	group := make([]string, entry.Devices)
	for i := range group {
		group[i] = strconv.Itoa(i)
	}
	return fmt.Sprintf("--device-group %s --max-model-len %d --max-num-seqs %d --max-seq-len-to-capture %d\n",
		strings.Join(group, ","), entry.Context, entry.Batch, entry.Prefill)
}

// extractQPC unpacks a gzipped tar into dir. It accepts only directories and
// regular files, all inside dir.
func extractQPC(r io.Reader, dir string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name := filepath.Clean(filepath.FromSlash(hdr.Name))
		if filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			return fmt.Errorf("archive entry %q is outside the QPC directory", hdr.Name)
		}
		target := filepath.Join(dir, name)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(f, tr)
			closeErr := f.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		default:
			return fmt.Errorf("archive entry %q has unsupported type %q", hdr.Name, hdr.Typeflag)
		}
	}
}

// qpcProgress counts downloaded bytes and reports each new whole percent.
type qpcProgress struct {
	e           *Executor
	engine      string
	model       string
	total       uint64
	done        uint64
	watchdog    *pullProgressWatchdog
	lastPercent int
}

func (p *qpcProgress) Write(b []byte) (int, error) {
	p.done += uint64(len(b))
	p.watchdog.recordProgress("qpc", int64(p.done))
	percent := 0
	if p.total > 0 {
		percent = int(min(p.done*100/p.total, 99))
	}
	if percent != p.lastPercent {
		p.lastPercent = percent
		p.e.emitPullProgress(ProgressEvent{Engine: p.engine, Op: "pull", Stage: "pulling", Percent: percent, Message: p.model})
	}
	return len(b), nil
}
