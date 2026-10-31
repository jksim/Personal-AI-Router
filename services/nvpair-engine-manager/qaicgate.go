// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"nvpair-shared/engines"
)

// qaicDockerTimeout bounds the Docker check, so a hung daemon cannot hold an
// install or a start.
const qaicDockerTimeout = 5 * time.Second

// checkQAICHostNow runs the full readiness check, Docker included.
func checkQAICHostNow() (qaicHostReady, []qaicHostProblem) {
	return currentQAICHost(dockerUsable())
}

// dockerUsable reports whether this user can reach the Docker daemon without sudo.
func dockerUsable() error {
	ctx, cancel := context.WithTimeout(context.Background(), qaicDockerTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "version", "--format", "{{.Server.Version}}").CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// qaicGate refuses an install or a start of the Qualcomm engine that this host
// cannot support. model is the model to start, or "" for an install. Other
// engines pass through unchecked.
func (e *Executor) qaicGate(engine, model string) error {
	if engine != engines.NameQaic {
		return nil
	}
	ready, problems := e.qaicHost()
	if !ready.ok() {
		var parts []string
		for _, p := range problems {
			if p.Blocking {
				parts = append(parts, p.Message+" "+p.Fix)
			}
		}
		return errors.New(strings.Join(parts, " "))
	}
	if model == "" {
		return nil
	}
	entries, err := qaicCatalogEntries()
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Model != model {
			continue
		}
		if entry.Devices > ready.SoCCount {
			return fmt.Errorf("%s is compiled for %d SoCs, and this card has %d that answered.", model, entry.Devices, ready.SoCCount)
		}
		if entry.Devices > 1 && !ready.P2PAllowed {
			for _, p := range problems {
				if !p.Blocking {
					return fmt.Errorf("%s needs %d SoCs that reach each other. %s %s", model, entry.Devices, p.Message, p.Fix)
				}
			}
		}
	}
	return nil
}
