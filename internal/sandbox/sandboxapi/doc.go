// Package sandboxapi holds the generated sandboxd OpenAPI client and types.
// The openapi.yaml tracks docker/sandboxes main (OpenAPI spec version 0.21.0).
// Pinned commit: 1902718485ad8efff942ac2851b93710ec6c8626 (2026-06-29).
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

// APIVersion is the OpenAPI spec version embedded in the vendored openapi.yaml
// (from docker/sandboxes main, commit 1902718485ad8efff942ac2851b93710ec6c8626).
const APIVersion = "0.21.0"

// SandboxdCommit is the docker/sandboxes commit the openapi.yaml was taken from.
const SandboxdCommit = "1902718485ad8efff942ac2851b93710ec6c8626"
