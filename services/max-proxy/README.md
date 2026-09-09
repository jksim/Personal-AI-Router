<!--
SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
SPDX-License-Identifier: Apache-2.0
-->

# MAX Proxy

A discovery-aware HTTP reverse proxy for [Modular MAX](https://docs.modular.com/max/)
nodes on the local network.

The binary is a profile literal and one call into
[`openai-proxy`](../openai-proxy/README.md), which holds all of the behaviour —
routing, model eligibility, failover, the scheduler priority list, the cluster
mTLS ingress, and the JSON-RPC control plane. That behaviour is documented in
[`lmstudio-proxy`](../lmstudio-proxy/README.md), which is the same code. Only
this engine's identity lives here:

| | |
| --- | --- |
| Engine tag | `max` |
| Discovery service key | `mx` |
| Compatibility port | `8000` (MAX's own default) |
| Managed backend port | `8001` |
| Persisted port file | `max-proxy-port.json` |
| Inference routes | `/v1/chat/completions`, `/v1/completions`, `/v1/embeddings` |
| Model-list route | `/v1/models` |

## Build

```bash
go build -o max-proxy .
```

## Usage

```
max-proxy [flags]
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--port` | `8000` | HTTP listen port for request forwarding |
| `--ignore-persisted-port` | `false` | Use `--port` even when a prior runtime port was saved |
| `--ipc` | *(empty — use stdio)* | Path to a Unix domain socket or Windows named pipe for IPC |
| `--cluster-dir` | *(empty)* | Cluster trust directory (`node.crt`/`node.key` plus trusted pins). Enables the LAN mTLS inference ingress while this node is a cluster member; empty means no ingress and no peer candidates. |
| `--log-level` | *(`$NVPAIR_LOG_LEVEL`, else `info`)* | Initial log level: `debug`, `info`, `warn`, or `error`. Changeable at runtime with `log/set-level`. |
| `--version` | | Print version and exit |

## Ports

The proxy squats MAX's own default port so an OpenAI client pointed at
`http://localhost:8000` reaches the router rather than one node's engine, and
the local engine is displaced to `8001`. This is the same facade-and-backend
shape Ollama uses on `11434`/`11435` and LM Studio on `1234`/`1235`.

Ollama and LM Studio are always engines `nvpair-engine-manager` knows about.
MAX need not be — the router can front a MAX engine some other tool is running,
or none at all. On a host where engine-manager has no MAX engine the broker runs
this proxy unmanaged: it takes `:8000` itself, its local backend stays cleared,
and no port-ownership warning is raised for a configuration that is not wrong.
See the [broker README](../nvpair-ui-broker/README.md#max-proxyget-status--max-proxysubscribe--max-proxyunsubscribe--max-proxymethod-generic-relay).

## Tests

```bash
go test ./...
```

This module holds the end-to-end test that builds and spawns the real binary,
plus the profile assertions. Everything else is tested in
[`openai-proxy`](../openai-proxy/README.md).
