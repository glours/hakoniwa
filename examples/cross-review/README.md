# README — cross-review example

## What this does

One producer agent generates an artifact; two independent reviewers evaluate
it in parallel (fan-out); a merger consolidates their findings (fan-in):

```
producer (claude) ──artifact.ready──▶ reviewer-alpha (codex) ──review.alpha──▶ merger (claude)
                                   ▶ reviewer-beta  (gemini) ──review.beta ──▶ merger
```

The merger starts only when **both** reviews have fired — this is the
fan-in join pattern supported natively by `depends_on` with multiple
`on_event` entries.

## Prerequisites

| Requirement | Notes |
|---|---|
| `sbx` CLI | `docker extension install docker/sandboxes` |
| Docker login | `docker login` |
| sbx login | `sbx login` |
| `ANTHROPIC_API_KEY` | For claude agents (producer, merger) |
| `OPENAI_API_KEY` | For the codex reviewer |
| `GOOGLE_API_KEY` | For the gemini reviewer |

## Usage

```bash
# Preview the execution plan:
hako plan

# Run the full pipeline:
hako up

# Stream reviewer output:
hako logs reviewer-alpha --follow

# Check statuses:
hako ps

# Clean up:
hako down
```

## Expected output

| Agent | Output path |
|---|---|
| producer | `/root/.hako/out/artifact.ready.json` |
| reviewer-alpha | `/root/.hako/out/review.alpha.json` |
| reviewer-beta | `/root/.hako/out/review.beta.json` |
| merger | `/root/.hako/out/merged.json` *(no channel emitted)* |

## Security posture

- `allow_widening: false` (the default) ensures reviewers **cannot** add
  `allow` egress rules beyond the project baseline. They can tighten
  with `deny` rules (they both deny telemetry hosts) but cannot loosen.
- Each reviewer's secrets are isolated: `OPENAI_API_KEY` is never
  injected into the gemini sandbox and vice versa.
- The `balanced` policy preset allows outbound HTTPS but blocks inbound
  connections and inter-sandbox direct traffic.
