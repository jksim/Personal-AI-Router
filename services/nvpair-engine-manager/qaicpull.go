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
	"sync"
)

// The Qualcomm engine's pull runs here rather than as a manifest command: the
// download must stream through SHA-256 and tar at once, because a QPC is tens
// of gigabytes and there may be no room for both the archive and its contents.
// It unpacks into <models_dir>/.partial, which neither the launcher nor
// list_models reads, and moves the QPC into place only after its SHA-256 matches.

// qaicPulls holds the models being pulled now and their partial directories.
// One pull runs per model, and a partial directory no running pull owns is a
// leftover from a crash.
var qaicPulls = struct {
	mu       sync.Mutex
	models   map[string]bool
	partials map[string]bool
}{models: map[string]bool{}, partials: map[string]bool{}}

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
	if err := e.qaicCheck(engine, entry.Model, false); err != nil {
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

	partialRoot := filepath.Join(modelsDir, ".partial")
	if err := os.MkdirAll(partialRoot, 0o755); err != nil {
		return nil, err
	}
	qaicPulls.mu.Lock()
	if qaicPulls.models[entry.Model] {
		qaicPulls.mu.Unlock()
		return nil, fmt.Errorf("%s is already being pulled", entry.Model)
	}
	removeLeftoverPartials(partialRoot)
	partial, err := os.MkdirTemp(partialRoot, strings.ReplaceAll(entry.Model, "/", "__")+"-")
	if err != nil {
		qaicPulls.mu.Unlock()
		return nil, err
	}
	qaicPulls.models[entry.Model] = true
	qaicPulls.partials[partial] = true
	qaicPulls.mu.Unlock()
	kept := false
	defer func() {
		qaicPulls.mu.Lock()
		delete(qaicPulls.models, entry.Model)
		delete(qaicPulls.partials, partial)
		qaicPulls.mu.Unlock()
		if !kept {
			_ = os.RemoveAll(partial)
		}
	}()
	if free, ok := freeBytes(partialRoot); ok && free < entry.UnpackedSize {
		return nil, fmt.Errorf("pull %s: it needs %d GB free, and %s has %d GB", entry.Model, entry.UnpackedSize>>30, modelsDir, free>>30)
	}

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
	// One byte over the catalog size is enough to refuse the file.
	limited := io.LimitReader(resp.Body, int64(entry.Size)+1)
	body := io.TeeReader(io.TeeReader(limited, counted), hash)
	tooLarge := fmt.Errorf("pull %s: the download is larger than the catalog's %d bytes; the file was refused", entry.Model, entry.Size)
	if err := extractQPC(body, partial, entry.UnpackedSize); err != nil {
		if counted.done > entry.Size {
			return nil, tooLarge
		}
		return nil, fmt.Errorf("pull %s: %w", entry.Model, watchdog.resolveError(pullCtx, err))
	}
	// gzip stops at its trailer; hash whatever follows too.
	if _, err := io.Copy(io.Discard, body); err != nil {
		return nil, fmt.Errorf("pull %s: %w", entry.Model, watchdog.resolveError(pullCtx, err))
	}
	if counted.done > entry.Size {
		return nil, tooLarge
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

// removeLeftoverPartials deletes partial directories that no running pull
// owns. The caller holds qaicPulls.mu.
func removeLeftoverPartials(root string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, entry := range entries {
		path := filepath.Join(root, entry.Name())
		if !qaicPulls.partials[path] {
			_ = os.RemoveAll(path)
		}
	}
}

// extractQPC unpacks a gzipped tar into dir. It accepts only directories and
// regular files, all inside dir, and no more than limit bytes in all.
func extractQPC(r io.Reader, dir string, limit uint64) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var written uint64
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
			n, copyErr := io.Copy(f, io.LimitReader(tr, int64(limit-written)+1))
			written += uint64(n)
			closeErr := f.Close()
			if copyErr == nil && written > limit {
				copyErr = fmt.Errorf("the archive unpacks to more than the catalog's %d bytes", limit)
			}
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
