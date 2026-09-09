// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

package openaiproxy

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"nvpair-shared/applog"
	"nvpair-shared/clustertrust"
)

// Run is the whole of an engine proxy's main(): flags, transport, signals,
// persisted port, and the serve loop. Each engine's binary is a profile literal
// and one call to this.
//
// The flag surface is identical for every engine on purpose. The broker spawns
// these proxies with the same arguments regardless of which engine they front,
// so an engine that invented its own flags would need broker changes for no
// gain.
func Run(profile Profile, version string) error {
	if err := profile.Validate(); err != nil {
		return err
	}

	port := flag.Int("port", profile.DefaultPort, "HTTP listen port")
	ignorePersistedPort := flag.Bool("ignore-persisted-port", false, "use --port even when a persisted port exists")
	ipcPath := flag.String("ipc", "", "IPC endpoint: Unix domain socket path or Windows named pipe (default: stdin/stdout)")
	clusterDir := flag.String("cluster-dir", "", "cluster trust directory (node.crt/key + trusted pins); enables the LAN mTLS inference ingress when this node is clustered")
	showVersion := flag.Bool("version", false, "print version and exit")
	resolveLevel := applog.RegisterFlag(nil, slog.LevelInfo)
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return nil
	}

	applog.Init(profile.LogName, resolveLevel())

	var transport io.ReadWriteCloser
	if *ipcPath != "" {
		conn, err := dialIPC(*ipcPath)
		if err != nil {
			return fmt.Errorf("connect to IPC endpoint %q: %w", *ipcPath, err)
		}
		transport = conn
		log.Printf("using IPC transport: %s", *ipcPath)
	} else {
		transport = newStdioTransport()
		log.Print("using stdio transport")
	}
	defer transport.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case sig := <-sigCh:
			log.Printf("received %s, shutting down", sig)
			cancel()
		case <-ctx.Done():
		}
	}()

	// Restore a previously chosen port (set via set-port) over the
	// --port/default, so the proxy comes back up where the user last put it.
	persisted, hasPersisted := profile.loadPersistedPort()
	effectivePort := profile.chooseStartupPort(*port, *ignorePersistedPort, persisted, hasPersisted)
	if hasPersisted && !*ignorePersistedPort && effectivePort == persisted {
		log.Printf("restored persisted proxy port %d", persisted)
	}

	codec := NewCodec(transport)
	disc := NewDiscovery()
	proxy := NewProxy(codec, disc, effectivePort, profile)
	if version != "" {
		proxy.version = version
	}
	// Open a live view of this node's cluster mTLS trust fabric. While unclustered
	// the proxy serves only the loopback plaintext personality; once this node is
	// a member the same listener also serves the pin-gated LAN mTLS ingress, and
	// peers become routable candidates. The proxy needs no restart to notice
	// either transition — it re-derives membership per request and on a watch.
	//
	// Membership is gated on an active admission or a pin, never on keypair
	// presence: a left/removed node keeps its keypair by design, and would
	// otherwise keep logging cluster_ingress with no cluster peers to serve.
	proxy.mesh = clustertrust.Open(*clusterDir)
	go proxy.mesh.Watch(ctx, func(clustered bool) {
		slog.Info("cluster inference ingress switched personality", "cluster_ingress", clustered)
		proxy.dropUnpinnedPeerTransports()
	})

	if err := proxy.Run(ctx); err != nil && ctx.Err() == nil {
		return fmt.Errorf("proxy: %w", err)
	}
	log.Print("shutdown complete")
	return nil
}
