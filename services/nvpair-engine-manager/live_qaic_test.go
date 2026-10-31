// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

//go:build live

// Live test of the Qualcomm engine on a real AI 100:
//
//	NVPAIR_LIVE_QAIC_ENGINE=1 go test -tags live -run TestLiveQaicLifecycle -v -timeout 3600s
//
// It needs the card powered on, Docker usable without sudo, and the Qwen3-30B-A3B
// 4-SoC QPC unpacked under the engine's models directory. Set
// NVPAIR_LIVE_QAIC_UNINSTALL=1 to also uninstall, which removes the 18 GB image.
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

const liveQaicModel = "Qwen/Qwen3-30B-A3B"

func TestLiveQaicLifecycle(t *testing.T) {
	if os.Getenv("NVPAIR_LIVE_QAIC_ENGINE") == "" {
		t.Skip("set NVPAIR_LIVE_QAIC_ENGINE=1 to run the Qualcomm engine on the real card")
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skipf("the qaic manifest declares linux/amd64 only; this is %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	writeLiveQaicLaunchArgs(t)

	reg := NewRegistry()
	if err := reg.LoadFS(bundledManifests, "manifests"); err != nil {
		t.Fatalf("load bundled manifests: %v", err)
	}
	m, ok := reg.Get("qaic")
	if !ok {
		t.Fatal("bundled qaic manifest missing")
	}
	plat := m.Platforms["linux/amd64"]
	plat.Runtime.Port = 0
	plat.Runtime.Model = liveQaicModel
	m.Platforms["linux/amd64"] = plat

	frames, stdin, stop := startManagerWithManifest(t, *m)
	defer stop()

	send(t, stdin, 1, "engine:install", map[string]string{"engine": "qaic"})
	if r := waitResult(t, frames, "1", 1800*time.Second); !strings.Contains(string(r), `"installed":true`) {
		t.Fatalf("expected installed:true after install, got %s", r)
	}

	start := func(id int) {
		t.Helper()
		startedAt := time.Now()
		send(t, stdin, id, "engine:start", map[string]string{"engine": "qaic"})
		if r := waitResult(t, frames, strconv.Itoa(id), 600*time.Second); !strings.Contains(string(r), `"running":true`) {
			t.Fatalf("expected running:true after start, got %s", r)
		}
		t.Logf("start %d reached /health in %s", id, time.Since(startedAt).Round(time.Second))
	}
	start(2)

	send(t, stdin, 3, "engine:action", map[string]any{"engine": "qaic", "action": "loaded_models"})
	if r := waitResult(t, frames, "3", 60*time.Second); !strings.Contains(string(r), liveQaicModel) {
		t.Errorf("loaded_models = %s, want %q", r, liveQaicModel)
	}
	send(t, stdin, 4, "engine:action", map[string]any{"engine": "qaic", "action": "list_models"})
	if r := waitResult(t, frames, "4", 60*time.Second); !strings.Contains(string(r), liveQaicModel) {
		t.Errorf("list_models = %s, want %q", r, liveQaicModel)
	}
	send(t, stdin, 5, "engine:action", map[string]any{"engine": "qaic", "action": "chat", "params": map[string]any{
		"model":    liveQaicModel,
		"messages": []map[string]string{{"role": "user", "content": "Say hello. /no_think"}},
	}})
	if r := waitResult(t, frames, "5", 120*time.Second); !strings.Contains(string(r), "choices") {
		t.Errorf("chat = %s, want a completion", r)
	}

	stoppedAt := time.Now()
	send(t, stdin, 6, "engine:stop", map[string]string{"engine": "qaic"})
	waitResult(t, frames, "6", 180*time.Second)
	t.Logf("stop took %s", time.Since(stoppedAt).Round(time.Second))
	if left := liveQaicContainers(t); left != "" {
		t.Errorf("containers survived engine:stop: %s", left)
	}

	// A crash: the container dies under the engine. The next start must still work.
	start(7)
	// docker kill often reports "did not receive an exit event": the driver
	// takes about 15 s to release the card after SIGKILL, longer than docker
	// waits. The container still dies, so wait for it to go.
	if out, err := exec.Command("docker", "kill", "nvpair-qaic").CombinedOutput(); err != nil {
		t.Logf("docker kill: %v: %s", err, strings.TrimSpace(string(out)))
	}
	deadline := time.Now().Add(90 * time.Second)
	for liveQaicContainers(t) != "" {
		if time.Now().After(deadline) {
			t.Fatal("the killed container did not go away")
		}
		time.Sleep(2 * time.Second)
	}
	time.Sleep(15 * time.Second)
	send(t, stdin, 12, "engine:get-installed", nil)
	if r := waitResult(t, frames, "12", 10*time.Second); !strings.Contains(string(r), `"engine":"qaic"`) || strings.Contains(qaicStatusOf(string(r)), `"running":true`) {
		t.Fatalf("after the crash the engine still reports running: %s", r)
	}
	start(8)
	send(t, stdin, 9, "engine:stop", map[string]string{"engine": "qaic"})
	waitResult(t, frames, "9", 180*time.Second)
	if left := liveQaicContainers(t); left != "" {
		t.Errorf("containers survived the second engine:stop: %s", left)
	}

	if os.Getenv("NVPAIR_LIVE_QAIC_UNINSTALL") != "" {
		send(t, stdin, 10, "engine:uninstall", map[string]string{"engine": "qaic"})
		waitResult(t, frames, "10", 300*time.Second)
		home, _ := os.UserHomeDir()
		if _, err := os.Stat(filepath.Join(home, ".local/share/nvpair/qaic-models", liveQaicModel)); err != nil {
			t.Errorf("uninstall removed the models directory: %v", err)
		}
	}
	send(t, stdin, 11, "shutdown", nil)
	waitResult(t, frames, "11", 10*time.Second)
}

// writeLiveQaicLaunchArgs writes the vLLM values for the hand-placed QPC, which
// pull_model writes once it downloads QPCs itself.
func writeLiveQaicLaunchArgs(t *testing.T) {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".local/share/nvpair/qaic-models", liveQaicModel, "qpc_16cores_128pl_8192cl_4devices_mxfp6_mxint8")
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("the Qwen3-30B-A3B QPC is not unpacked at %s", dir)
	}
	args := "--device-group 0,1,2,3 --max-model-len 8192 --max-num-seqs 1 --max-seq-len-to-capture 128\n"
	if err := os.WriteFile(filepath.Join(dir, "launch.args"), []byte(args), 0o644); err != nil {
		t.Fatal(err)
	}
}

// liveQaicContainers lists containers named nvpair-qaic. Empty is the expected result.
func liveQaicContainers(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("docker", "ps", "-a", "--filter", "name=nvpair-qaic", "--format", "{{.Names}} {{.Status}}").Output()
	if err != nil {
		t.Fatalf("docker ps: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// qaicStatusOf returns the qaic object from an engine:get-installed result.
func qaicStatusOf(result string) string {
	i := strings.Index(result, `"engine":"qaic"`)
	if i < 0 {
		return ""
	}
	j := strings.Index(result[i:], "}")
	if j < 0 {
		return result[i:]
	}
	return result[i : i+j]
}
