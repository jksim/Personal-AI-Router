// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

package openaiproxy

import (
	"fmt"

	"nvpair-shared/noderec"
)

// Profile is everything that differs between the engines this proxy can front.
//
// The body of this package is protocol and cluster behaviour: node selection,
// failover, reservations, the mTLS ingress, the transport pool. None of that
// varies by engine. What varies is an engine's identity on the wire and the
// handful of routes it answers on — and that is exactly this struct.
//
// Deliberately absent, because they are contracts rather than identity: the
// X-NVPAIR-Engine-Identity-Probe header and its 409 response code, the
// workload:* method names and envelope, the per-process run id (which exists
// precisely because several engine proxies each number their requests from 1),
// the JSON-RPC surface, and localBackend.Engine — that last one is inbound data
// from the broker, not this proxy's own identity.
type Profile struct {
	// Engine is the engine's identity in two places that must agree: the tag
	// on every workload this proxy reports, and the key its models are
	// attributed to in a peer's advertised inventory.
	Engine string

	// ServiceKey is the mDNS TXT key this proxy's port is advertised under
	// and the key it reads back from discovered nodes.
	ServiceKey noderec.ServiceKey

	// DefaultPort is the listen port before any --port flag or persisted
	// choice. LegacyPort is a one-time migration escape hatch for an engine
	// whose default moved; zero means the engine has no history to migrate.
	DefaultPort int
	LegacyPort  int

	// PortFile is the basename under the app data dir holding the sticky
	// port. Each engine needs its own, or two proxies fight over one file.
	PortFile string

	// LogName identifies this process in the log stream.
	LogName string

	// ErrorIDPrefix begins every sticky ServiceError id this proxy reports.
	// nvpair-errors matches these ids literally, so the prefix and the
	// reporting call site have to move together.
	ErrorIDPrefix string

	// InferenceEndpoints are the request paths that count as cluster
	// workloads. Anything else is forwarded without being reported as one.
	InferenceEndpoints map[string]bool

	// ModelListRoutes are the paths answered by fanning out across candidates
	// and merging, rather than forwarding to a single node.
	ModelListRoutes map[string]bool

	// FacadeMessage is returned when engine-manager probes this port for an
	// engine's identity. The federated model list must never satisfy such a
	// probe, or the engine would appear installed because its proxy answered.
	FacadeMessage string
}

// OpenAIInferenceEndpoints is the default inference route set for an engine
// serving the OpenAI API. Engines with a native API of their own extend it.
func OpenAIInferenceEndpoints() map[string]bool {
	return map[string]bool{
		"/v1/chat/completions": true,
		"/v1/completions":      true,
		"/v1/embeddings":       true,
	}
}

// OpenAIModelListRoutes is the default model-list route set.
func OpenAIModelListRoutes() map[string]bool {
	return map[string]bool{"/v1/models": true}
}

// Validate rejects a profile that would produce a proxy which looks healthy and
// behaves wrongly.
//
// Every field here is load-bearing on a wire contract: an empty engine tag
// silently unattributes workloads, an unset service key advertises under the
// zero key, a shared port file makes two proxies fight, and a missing error
// prefix collides with another engine's sticky error ids. None of these fail
// loudly at runtime, so they are rejected at startup instead.
func (p Profile) Validate() error {
	switch {
	case p.Engine == "":
		return fmt.Errorf("profile: engine name is required")
	case p.ServiceKey == "":
		return fmt.Errorf("profile: service key is required for engine %q", p.Engine)
	case p.DefaultPort <= 0 || p.DefaultPort > 65535:
		return fmt.Errorf("profile: engine %q has invalid default port %d", p.Engine, p.DefaultPort)
	case p.PortFile == "":
		return fmt.Errorf("profile: engine %q needs its own port file", p.Engine)
	case p.LogName == "":
		return fmt.Errorf("profile: engine %q needs a log name", p.Engine)
	case p.ErrorIDPrefix == "":
		return fmt.Errorf("profile: engine %q needs an error id prefix", p.Engine)
	case len(p.InferenceEndpoints) == 0:
		return fmt.Errorf("profile: engine %q declares no inference endpoints", p.Engine)
	case len(p.ModelListRoutes) == 0:
		return fmt.Errorf("profile: engine %q declares no model-list routes", p.Engine)
	case p.FacadeMessage == "":
		return fmt.Errorf("profile: engine %q needs a facade rejection message", p.Engine)
	}
	return nil
}

// upstreamUnreachableID is the sticky ServiceError id for a node whose engine
// this proxy could not reach. One id per node, so a second failure upserts
// rather than piling up, and clearing on success removes exactly this entry.
func (p Profile) upstreamUnreachableID(nodeID string) string {
	return p.ErrorIDPrefix + ":upstream-unreachable:" + nodeID
}
