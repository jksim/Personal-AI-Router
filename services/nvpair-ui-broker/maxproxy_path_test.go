// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveMaxProxyPath pins the asymmetry that makes an optional worker safe
// to run without.
//
// An explicit --max-proxy-path that does not resolve is an operator mistake and
// must be reported, because silently ignoring it would leave the operator
// believing MAX is running. An absent default sibling is not a mistake at all —
// it is every host that simply does not front a MAX engine — so it reports an
// error the caller degrades on rather than aborting the broker.
func TestResolveMaxProxyPath(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "max-proxy")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := resolveMaxProxyPath(binary)
	if err != nil {
		t.Fatalf("explicit path that exists: %v", err)
	}
	if got != binary {
		t.Fatalf("resolved %q, want %q", got, binary)
	}

	missing := filepath.Join(dir, "not-here")
	if _, err := resolveMaxProxyPath(missing); err == nil {
		t.Fatal("an explicit path that does not exist must be reported, not ignored")
	} else if !strings.Contains(err.Error(), "max-proxy") {
		t.Fatalf("error %q does not name the binary the operator asked for", err)
	}

	// With no override, resolution looks for a sibling in the working
	// directory. The message must name the flag, since that is the operator's
	// way out.
	t.Chdir(t.TempDir())
	if _, err := resolveMaxProxyPath(""); err == nil {
		t.Fatal("expected an error with no sibling binary present")
	} else if !strings.Contains(err.Error(), "--max-proxy-path") {
		t.Fatalf("error %q does not mention the override flag", err)
	}
}

// TestNewBrokerCarriesEveryWorkerPath pins that every resolved worker path
// reaches the broker field the supervisor reads.
//
// The resolver and the supervisor can both be correct while nothing connects
// them: max-proxy shipped for one commit with a flag, a resolver, a workerPaths
// entry and a startup block, and no assignment between the struct and the
// field. Everything compiled, the resolver test passed, and the broker logged
// "path not resolved" for a binary it had just been handed. Asserting the whole
// struct rather than one field is what makes that class of gap visible.
func TestNewBrokerCarriesEveryWorkerPath(t *testing.T) {
	paths := workerPaths{
		scanner:       "scanner-bin",
		nodeInfo:      "node-info-bin",
		proxy:         "proxy-bin",
		lmstudioProxy: "lmstudio-proxy-bin",
		maxProxy:      "max-proxy-bin",
		workloadMgr:   "workload-manager-bin",
		errors:        "errors-bin",
		engineMgr:     "engine-manager-bin",
		manualNodes:   "manual-nodes-bin",
		settings:      "settings-bin",
		clusterMgr:    "cluster-manager-bin",
		scheduler:     "scheduler-bin",
		clusterDir:    "cluster-dir",
	}
	b := NewBroker(nil, paths)
	got := map[string]string{
		"scanner":          b.scannerPath,
		"node-info":        b.nodeInfoPath,
		"proxy":            b.proxyPath,
		"lmstudio-proxy":   b.lmstudioProxyPath,
		"max-proxy":        b.maxProxyPath,
		"workload-manager": b.workloadMgrPath,
		"errors":           b.errorsPath,
		"engine-manager":   b.engineMgrPath,
		"manual-nodes":     b.manualNodesPath,
		"settings":         b.settingsPath,
		"cluster-manager":  b.clusterMgrPath,
		"scheduler":        b.schedulerPath,
		"cluster-dir":      b.clusterDir,
	}
	want := map[string]string{
		"scanner":          paths.scanner,
		"node-info":        paths.nodeInfo,
		"proxy":            paths.proxy,
		"lmstudio-proxy":   paths.lmstudioProxy,
		"max-proxy":        paths.maxProxy,
		"workload-manager": paths.workloadMgr,
		"errors":           paths.errors,
		"engine-manager":   paths.engineMgr,
		"manual-nodes":     paths.manualNodes,
		"settings":         paths.settings,
		"cluster-manager":  paths.clusterMgr,
		"scheduler":        paths.scheduler,
		"cluster-dir":      paths.clusterDir,
	}
	for name, wantPath := range want {
		if got[name] != wantPath {
			t.Errorf("%s path = %q, want %q", name, got[name], wantPath)
		}
	}
}
