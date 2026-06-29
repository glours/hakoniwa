# hakoniwa

![Status: experimental](https://img.shields.io/badge/status-experimental-orange)

## Why Hakoniwa

**Hakoniwa** (箱庭, pronounced *ha-ko-NI-wa*) takes its name from a Japanese word built from two kanji: 箱 (*hako*, "box") and 庭 (*niwa*, "garden"). Together they describe a miniature garden arranged inside a tray — a composed, self-contained world where stones, sand, and plants are orchestrated within a defined and controlled space.

In Japanese, 箱庭 is also the literal word for "sandbox" in technology and gaming. The metaphor maps precisely onto what this tool does: it lets you orchestrate heterogeneous AI agents in isolated sandboxes, composed together into a coherent application. Each agent lives in its own *hako* (box); the *niwa* (garden) is the arrangement you declare in a single YAML file.

The short form **`hako`** (箱 = box) is the name of the CLI binary — a nod to the sandbox primitive at the heart of every session.

## Vision

A Compose-like declarative tool for Docker Sandboxes. Describe a multi-agent application in a YAML file (`hakoniwa.yaml` or `hako.yaml`), where each AI agent runs in its own isolated sandbox, and agents communicate and react to events emitted by others.

## Status

> **Experimental** — not production-ready. Breaking changes are expected at any time. APIs, file formats, and CLI commands may change without notice.

## File Formats

`hako` accepts the following file names, in order of precedence:

- `hakoniwa.yaml` / `hakoniwa.yml`
- `hako.yaml` / `hako.yml`
- `.sbxenv` (compatibility with [`docker/sbxenv`](https://github.com/docker/sbxenv))

## Quick Example

```yaml
# hakoniwa.yaml
name: bugfix-session

defaults:
  policy:
    default: balanced

channels: [ repro.ready, fix.ready ]

agents:
  reproducer:
    agent: claude
    template: node
    resources: { cpus: 4, memory: 8192 }
    ports: [ "8080:8080" ]
    secrets:
      - { value: "gh auth token", env: GH_TOKEN, host: api.github.com }
    emits: [ repro.ready ]

  fixer:
    agent: codex
    depends_on:
      reproducer: { condition: on_event, channel: repro.ready }
    subscribes: [ repro.ready ]
    reach: [ "reproducer:8080" ]
    policy:
      network: { allow: [ "*.github.com" ], deny: [ "*.telemetry.io" ] }
    emits: [ fix.ready ]

  test-writer:
    agent: gemini
    depends_on:
      fixer: { condition: on_event, channel: fix.ready }
    subscribes: [ fix.ready ]
    kits: [ ./kits/test-runner ]
```

## CLI

| Command | Description |
|---|---|
| `hako up` | Start all agents defined in the file |
| `hako down` | Stop and remove all running agents |
| `hako plan` | Show the execution plan without starting anything |
| `hako logs <agent>` | Stream logs from a specific agent |
| `hako ps` | List running agents and their status |

## References

- [docker/sandboxes](https://github.com/docker/sandboxes) — Docker Sandboxes runtime
- [docker/compose](https://github.com/docker/compose) — Docker Compose
- [docker/sbxenv](https://github.com/docker/sbxenv) — Sandbox environment file format
