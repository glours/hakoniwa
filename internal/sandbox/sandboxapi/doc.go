// Package sandboxapi holds the generated sandboxd OpenAPI client and types.
// The openapi.yaml is from the docker/sandboxes v0.34.0 release (OpenAPI spec
// version 0.16.0).
//
// To regenerate after editing openapi.yaml:
//
//	oapi-codegen --config cfg-types.yaml openapi.yaml
//	oapi-codegen --config cfg-client.yaml openapi.yaml
//
// Streaming and HTTP-upgrade endpoints (getFile, putFile, attachExec,
// sessionHold) are excluded from the generated client and implemented by
// hand in the parent package when needed.
package sandboxapi

//go:generate oapi-codegen --config cfg-types.yaml openapi.yaml
//go:generate oapi-codegen --config cfg-client.yaml openapi.yaml

// APIVersion is the OpenAPI spec version embedded in the vendored openapi.yaml
// (from docker/sandboxes release v0.34.0).
const APIVersion = "0.16.0"

// SandboxdRelease is the docker/sandboxes release tag the openapi.yaml was
// taken from.
const SandboxdRelease = "v0.34.0"
