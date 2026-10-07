// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"
)

// maxPort is MAX's default OpenAI-API server port, probed the same way Ollama
// is hardcoded to 11434 and LM Studio to 1234. A manual node is remote, so (like
// the others) we assume the engine's default port rather than resolving it via
// the engine manager, which only governs the local engine.
const maxPort = 8000

// probeMax reports whether a MAX server is answering at addr:port, and the
// models it serves.
//
// This is probeLMStudio's logic against a different port: both engines serve the
// OpenAI API, so both are probed with GET /v1/models. It is duplicated rather
// than shared so each engine's probe carries its own name in the logs and can
// diverge — MAX runs one model per process, and a future probe may want to say
// which — without disturbing an engine it no longer resembles.
func (m *Manager) probeMax(addr string, port int) (bool, []string) {
	url := "http://" + net.JoinHostPort(addr, strconv.Itoa(port)) + "/v1/models"
	start := time.Now()
	resp, err := m.client.Get(url)
	if err != nil {
		slog.Debug("manual probe max failed",
			"addr", addr, "port", port, "duration_ms", time.Since(start).Milliseconds(), "err", err)
		return false, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		slog.Debug("manual probe max non-OK",
			"addr", addr, "port", port, "status", resp.StatusCode,
			"duration_ms", time.Since(start).Milliseconds())
		return false, nil
	}
	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		// Reachable, but the model list didn't parse — still report it up.
		slog.Debug("manual probe max up (models parse failed)",
			"addr", addr, "port", port, "err", err)
		return true, nil
	}
	models := make([]string, 0, len(result.Data))
	for _, d := range result.Data {
		if d.ID != "" {
			models = append(models, d.ID)
		}
	}
	slog.Debug("manual probe max up",
		"addr", addr, "port", port, "models", len(models),
		"duration_ms", time.Since(start).Milliseconds())
	return true, models
}
