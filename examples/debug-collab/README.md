# README — debug-collab example

## What this does

Three AI agents collaborate end-to-end to investigate and fix a bug:

```
reproducer (claude) ──repro.ready──▶ fixer (codex) ──fix.ready──▶ test-writer (gemini)
         │                                  │
         │ HTTP :8080 ◀────────────── reach │
         └── diagnostic endpoint           └── patch + explanation
```

1. **reproducer** — checks out the failing code, runs it, and exposes a live HTTP
   diagnostic endpoint on port 8080 so the fixer can inspect state at runtime.
   On completion it writes `{"issue": "...", "steps": [...]}` to
   `/root/.hako/out/repro.ready.json`.

2. **fixer** — waits for `repro.ready`, reads the JSON payload from
   `/root/.hako/in/repro.ready.json`, and can also probe the live service via
   `$HAKO_REACH_REPRODUCER_8080` (injected by the orchestrator as
   `host.docker.internal:<port>`).
   On completion it writes a patch JSON to `/root/.hako/out/fix.ready.json`.

3. **test-writer** — waits for `fix.ready`, reads the patch, and generates a
   regression test suite. Writes test files to `/root/.hako/out/tests.json`.

## Prerequisites

| Requirement | Notes |
|---|---|
| `sbx` CLI | `docker extension install docker/sandboxes` |
| Docker login | `docker login` |
| sbx login | `sbx login` |
| `ANTHROPIC_API_KEY` | For the claude agent |
| `OPENAI_API_KEY` | For the codex agent |
| `GOOGLE_API_KEY` | For the gemini agent |
| `GH_TOKEN` *(optional)* | For private repo access; falls back gracefully |

## Usage

```bash
# Check what would happen (no mutations):
hako plan

# Run the full pipeline:
hako up

# Stream a specific agent's output live:
hako logs fixer --follow

# Check agent statuses:
hako ps

# Tear everything down:
hako down
```

## Expected output

Each agent writes its results to `.hako/out/<channel>.json` inside its sandbox.
The orchestrator reads and stages these files into `.hako/in/<channel>.json`
for the next agent before starting it.

Final artefacts:
- `debug-collab-fixer` sandbox: `/root/.hako/out/fix.ready.json`
- `debug-collab-test-writer` sandbox: `/root/.hako/out/tests.json`

## Network policy

All agents use the `balanced` preset (outbound HTTPS allowed, inbound blocked).
Per-agent `allow` rules extend the baseline selectively:

| Agent | Allowed hosts |
|---|---|
| reproducer | `api.github.com`, `*.githubusercontent.com` |
| fixer | `api.github.com`, `registry.npmjs.org` |
| test-writer | `api.github.com`, `registry.npmjs.org`, `pypi.org` |

## `reach` wiring

The fixer uses `reach: ["reproducer:8080"]`. The orchestrator publishes
`reproducer:8080` and injects `HAKO_REACH_REPRODUCER_8080=host.docker.internal:<host_port>`
into the fixer's session so it can inspect the live diagnostic service.

> **macOS note:** `host.docker.internal` resolves to the host from inside the
> sandbox. On Linux the gateway IP is used instead if the hostname is not
> available.
