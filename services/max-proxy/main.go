// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

// Command max-proxy fronts this cluster's Modular MAX engines.
//
// The proxy itself lives in openai-proxy; this binary is only MAX's identity.
// MAX serves the OpenAI API, so it needs no route overrides — an engine with a
// native API of its own would extend the defaults here.
package main

import (
	"log"

	openaiproxy "openai-proxy"

	"nvpair-shared/noderec"
)

// Version is stamped at build time via -ldflags "-X main.Version=...".
var Version = "dev"

// maxProfile is MAX's identity on the wire.
//
// Port 8000 is MAX's own documented default, and the proxy takes it as its
// facade so an unmodified OpenAI client pointed at localhost:8000 reaches the
// cluster; the broker moves the engine itself to 8001. There is no legacy port
// because this engine has no deployed history to migrate.
var maxProfile = openaiproxy.Profile{
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
	if err := openaiproxy.Run(maxProfile, Version); err != nil {
		log.Fatalf("max-proxy: %v", err)
	}
}
