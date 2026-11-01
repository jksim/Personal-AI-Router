// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	_ "embed"
	"os"
	"path/filepath"
)

// qaicLauncher is the launcher this engine manager carries. The install
// writes the same bytes from the manifest; a test keeps the two equal.
//
//go:embed launchers/qaic-serve.sh
var qaicLauncher []byte

// refreshQaicLauncher replaces an installed launcher that differs from
// qaicLauncher, so a PAIR upgrade updates the launcher an older install wrote.
// It does nothing where the engine is not installed.
func refreshQaicLauncher(installDir string) error {
	path := filepath.Join(installDir, "qaic-serve")
	current, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if bytes.Equal(current, qaicLauncher) {
		return nil
	}
	tmp, err := os.CreateTemp(installDir, ".qaic-serve-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(qaicLauncher); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
