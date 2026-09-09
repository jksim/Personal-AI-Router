// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// lmstudioModelsDir is the default on-disk model cache for LM Studio.
func lmstudioModelsDir() string {
	return expandPath("~/.lmstudio/models")
}

// hfCacheDir is where the HuggingFace libraries put downloaded repos, which is
// where a MAX model lives. HF_HOME wins over the default, matching the
// libraries' own precedence, so a user who moved their cache is not told their
// models are missing.
//
// The path is set explicitly rather than inferred because MAX's own docs
// disagree about it, and a wrong guess here silently lists nothing.
func hfCacheDir() string {
	if home := strings.TrimSpace(os.Getenv("HF_HOME")); home != "" {
		return filepath.Join(expandPath(home), "hub")
	}
	if cache := strings.TrimSpace(os.Getenv("HF_HUB_CACHE")); cache != "" {
		return expandPath(cache)
	}
	return expandPath("~/.cache/huggingface/hub")
}

// engineModelsDir is the directory an engine's model actions operate on.
//
// This used to hand every engine LM Studio's directory, which was harmless only
// because LM Studio was the sole engine with a path-based model action. A
// second one made it wrong: MAX's models live in the HuggingFace cache, and a
// remove_path rooted at ~/.lmstudio/models would either find nothing or, worse,
// confine a delete to the wrong tree.
func engineModelsDir(engine string) string {
	switch engine {
	case "max":
		return hfCacheDir()
	default:
		return lmstudioModelsDir()
	}
}

// safeRemoveUnderRoot deletes target after verifying it resolves under root.
// Both paths are cleaned; symlinks on target are evaluated before the confinement
// check so a path cannot escape the allowed root via symlink tricks.
func safeRemoveUnderRoot(root, target string) error {
	if strings.TrimSpace(root) == "" || strings.TrimSpace(target) == "" {
		return fmt.Errorf("remove_path: root and path are required")
	}
	absRoot, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return fmt.Errorf("remove_path: root: %w", err)
	}
	absTarget, err := filepath.Abs(filepath.Clean(target))
	if err != nil {
		return fmt.Errorf("remove_path: target: %w", err)
	}
	evalRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("remove_path: root symlink: %w", err)
		}
		evalRoot = absRoot
	}
	evalTarget, err := filepath.EvalSymlinks(absTarget)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("remove_path: target symlink: %w", err)
		}
		evalTarget = absTarget
	}
	if !pathWithinRoot(evalRoot, evalTarget) {
		return fmt.Errorf("remove_path: %q escapes allowed root %q", evalTarget, evalRoot)
	}
	if _, err := os.Stat(evalTarget); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("remove_path: %q does not exist", evalTarget)
		}
		return fmt.Errorf("remove_path: stat %q: %w", evalTarget, err)
	}
	if err := os.RemoveAll(evalTarget); err != nil {
		return fmt.Errorf("remove_path: %w", err)
	}
	return nil
}

func pathWithinRoot(root, target string) bool {
	root = filepath.Clean(root)
	target = filepath.Clean(target)
	if target == root {
		return true
	}
	prefix := root + string(os.PathSeparator)
	return strings.HasPrefix(target, prefix)
}
