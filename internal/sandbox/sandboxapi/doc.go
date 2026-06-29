// Package sandboxapi holds the generated sandboxd OpenAPI client and types.
// The openapi.yaml is pinned at version 0.21.0 from docker/sandboxes.
//
// To regenerate after editing openapi.yaml:
//
//	oapi-codegen --config cfg-types.yaml openapi.yaml
//	oapi-codegen --config cfg-client.yaml openapi.yaml
//
// Streaming and HTTP-upgrade endpoints (getFile, putFile, attachExec,
// attachAgentSession, sessionHold) are excluded from the generated client
// and implemented by hand in the parent package when needed.
package sandboxapi

//go:generate oapi-codegen --config cfg-types.yaml openapi.yaml
//go:generate oapi-codegen --config cfg-client.yaml openapi.yaml

// APIVersion is the pinned sandboxd OpenAPI version this client was generated from.
const APIVersion = "0.21.0"
