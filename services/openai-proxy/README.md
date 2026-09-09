<!--
SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
SPDX-License-Identifier: Apache-2.0
-->

# OpenAI Proxy (library)

The discovery-aware inference proxy, as a Go library rather than a binary. A
consuming binary supplies a `Profile` and calls `Run`; everything else — node
selection, eligibility, failover, the scheduler priority list, reservations, the
cluster mTLS ingress, the transport pool, the whole JSON-RPC control plane — is
this package.

This module is not part of upstream NVIDIA Personal AI Router. It exists because
adding engines meant adding proxies, and the existing two are near-identical:
`transport.go`, `codec.go` and `ipc.go` are byte-identical between them, and
only 23 lines of `ollama-proxy` and 10 of `lmstudio-proxy` mention their engine
at all. A third and fourth hand-fork would have been four near-copies of the
routing and failover engine. Upstream's two proxies are deliberately left
untouched, so rebases stay clean.

## What a profile carries

`Profile` is exactly what differs between engines, and nothing else:

| Field | Meaning |
| --- | --- |
| `Engine` | the tag on every workload this proxy reports, and the key its models are attributed to in a peer's inventory |
| `ServiceKey` | the mDNS TXT key its port is advertised under and read back from |
| `DefaultPort` / `LegacyPort` | the listen port before any flag or persisted choice; the legacy port is a one-time migration for an engine whose default moved, and is zero for a new one |
| `PortFile` | the sticky-port file under the app data dir — each engine needs its own, or two proxies fight over one file |
| `LogName` | this process's name in the log stream |
| `ErrorIDPrefix` | the prefix on every sticky `ServiceError` id it reports; `nvpair-errors` matches these literally |
| `InferenceEndpoints` | the request paths that count as cluster workloads |
| `ModelListRoutes` | the paths answered by fanning out across candidates and merging |
| `FacadeMessage` | what an engine-identity probe is told, so a federated model list can never make an engine look installed |

`Validate` rejects a profile at startup rather than at runtime, because every
one of those fields fails silently: an empty engine tag unattributes workloads,
an unset service key advertises under the zero key, a shared port file makes two
proxies fight, and a duplicate error prefix collides with another engine's
sticky ids.

Deliberately **not** in the profile, because they are protocol contracts rather
than identity: the `X-NVPAIR-Engine-Identity-Probe` header and its 409 response,
the `workload:*` method names and envelope, the per-process run id (which exists
*because* several engine proxies each number their requests from 1), the
JSON-RPC surface itself, and `localBackend.Engine` — that last one is inbound
data from the broker, not this proxy's own identity.

## Using it

```go
var profile = openaiproxy.Profile{
    Engine:             "max",
    ServiceKey:         noderec.ServiceMax,
    DefaultPort:        8000,
    PortFile:           "max-proxy-port.json",
    LogName:            "max-proxy",
    ErrorIDPrefix:      "max-proxy",
    InferenceEndpoints: openaiproxy.OpenAIInferenceEndpoints(),
    ModelListRoutes:    openaiproxy.OpenAIModelListRoutes(),
    FacadeMessage:      "the compatibility facade is not a MAX engine",
}

func main() {
    if err := openaiproxy.Run(profile, Version); err != nil {
        log.Fatal(err)
    }
}
```

`Run` is the whole of a proxy's `main()`: flags, transport, signals, persisted
port, and the serve loop. The flag surface is identical for every engine on
purpose — the broker spawns these proxies with the same arguments regardless of
which engine they front, so an engine that invented its own flags would need
broker changes for no gain.

For the behaviour itself — routing, eligibility, failover, cluster ingress, the
control-plane methods — see [`lmstudio-proxy`](../lmstudio-proxy/README.md),
which documents the same code.

## Consumers

- [`max-proxy`](../max-proxy/README.md) — Modular MAX

## Tests

```bash
go test ./...
```

The end-to-end test that spawns a real proxy process lives in `max-proxy`, not
here: a library has no binary to spawn.
