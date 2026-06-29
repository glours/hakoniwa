# Hakoniwa — Design

Hakoniwa (箱庭) is a Compose-like orchestrator for Docker Sandboxes. A single
declarative file (`hakoniwa.yaml` / `hako.yaml`) describes a multi-agent
application: each AI agent runs in its own isolated sandbox, agents declare
dependencies on one another, and they hand off work through named event
channels. The CLI is `hako`.

This document records the design decisions for the v0 of the tool and the
reasoning behind them. It is grounded in a read of the `docker/sandboxes`
(`sandboxlib`, `sandboxd`/`sandboxapi`, `cli-plugin`) and `docker/sbxenv`
repositories; facts attributed to those repos were verified against the
source. Where a capability does not exist today it is called out explicitly
and tracked as an upstream issue candidate.

Conventions used below:

- **Verified** — confirmed by reading the Sandboxes/sbxenv source this cycle.
- **Net-new** — must be built in Hakoniwa; no equivalent exists upstream today.
- **Phase 1** — in scope for the first end-to-end milestone (use cases #2 and
  #4). **Phase 2** — deliberately deferred.

---

## 1. Integration with Sandboxes

Sandboxes exposes three surfaces a client can build on:

- **`sbx` CLI** (`cli-plugin`) — the user-facing command set (`create`, `run`,
  `ls`, `rm`, `stop`, `exec`, `ports`, `secret`, `kit`, `policy`, `daemon`, …).
  It auto-starts the daemon, authenticates against Docker Hub, discovers
  credentials, and resolves kit references. *(Verified.)*
- **`sandboxlib`** — the Go library the CLI and daemon are built from
  (`runtime`, `sandbox`, `kit`, `secretresolver`, `credentialresolver`, …). It
  is an internal module (`github.com/docker/sandboxes`) with no versioned
  public API; it is consumed only inside the monorepo. *(Verified.)*
- **`sandboxd`** — the daemon, reached over a Unix socket, serving an
  OpenAPI-described HTTP API (`sandboxapi`, generated client/server). Auth is a
  Docker Hub JWT taken from the local Docker credential store. *(Verified:
  `sandboxapi/openapi.yaml` is version `0.21.0`.)*

### Tradeoffs

| Dimension | Shell out to `sbx` | Link `sandboxlib` (Go) | HTTP to `sandboxd` (OpenAPI) | **Hybrid (chosen)** |
|---|---|---|---|---|
| Coupling | Loose — depends on CLI flag surface only | Tight — compiles against internal packages | Medium — depends on the OpenAPI contract | CLI for bootstrap + API contract for the loop |
| Versioning/stability | Flag surface drifts; no compat guarantee | **No versioned API**; churns with the monorepo | `openapi.yaml` is the closest thing to a stable contract (v0.21.0) | Pin the OpenAPI version; tolerate CLI flags |
| Idempotence | `sbx run` already does find-or-create by derived name | Hand-rolled | Names are the key; `createSandbox` returns `409` on conflict | Name-based find-or-create + structured `409` |
| Performance | Process spawn per call; text parsing | In-process, fastest | One socket round-trip per call | Spawn only on the cold path; API on the hot path |
| Error surface | Text on stderr; JSON only on `ls`/`inspect` | Go errors, richest | Typed JSON errors + HTTP status codes | Structured where it matters (the loop) |
| Install constraints | Just the `sbx` plugin on `PATH` | Vendoring + `go.mod` lockstep with the monorepo | Daemon must be running + reachable socket | `sbx` present **and** daemon reachable |
| Language | Any | Go only | Any | Any (Go preferred for the generated client) |
| Streaming (for events) | None | Possible but bespoke | `agent/session` & `exec/attach` are HTTP-upgrade streams | API stream taps drive the event layer |

### Two facts that decide it

Two verified constraints rule out a pure-HTTP client and rule out reimplementing
the CLI:

1. **The daemon cannot resolve kit references.** `SandboxCreateRequest.kits` is
   *rejected* by the daemon — it has "no OCI pull, no git clone, no filesystem
   access outside its state dir." Clients must resolve `--kit` refs themselves
   and ship the parsed content as `kit_artifacts` (the JSON form of
   `sandboxlib/kit.Artifact`). *(Verified — `openapi.yaml` `SandboxCreateRequest`.)*
2. **The daemon cannot read the user's shell environment.** Env-var-based
   credentials must be read client-side and passed as `credential_values`; the
   daemon "cannot access shell env vars." *(Verified.)*

Both resolution steps (kit refs, credential discovery) live in `sandboxlib` and
are already wired into `sbx create`. A pure-HTTP orchestrator would have to
re-implement them or vendor those internal packages — exactly the coupling the
OpenAPI route was supposed to avoid.

### Decision: hybrid

**Recommendation (validated): a hybrid client.** Shell out to `sbx` for the
*cold path* — anything that is host-global, one-time, or requires the
resolution logic that lives in `sandboxlib` (daemon lifecycle, auth, secret and
kit resolution, credential discovery, initial sandbox creation). Talk to
`sandboxd` over the generated OpenAPI client for the *hot path* — the
per-sandbox, repeated, structured steps of the orchestration loop
(`inspect` → `start`/`stop`/`delete` → `agent/session`/`exec` → `ports` →
`policy` → `files`), where we need JSON, a machine-readable status for
dependency gating, and HTTP-upgrade streams for the event layer.

This mirrors how Docker Compose itself integrates with the engine: Compose
speaks to `dockerd` through the API rather than shelling out to `docker`. We do
the same for the loop, while delegating the resolution that Sandboxes has
chosen to keep client-side to the tool that already implements it.

> **One boundary to revisit in phase 2.** Initial creation sits on the `sbx`
> side in phase 1 so it inherits credential discovery + kit resolution. It can
> migrate to `POST /sandbox` later if Hakoniwa pre-resolves those itself by
> importing only `sandboxlib/{kit,secretresolver,credentialresolver}`. That is
> the single seam where the line may move; everything else is settled.

## 2. Dependency inventory

This is the exact set of Sandboxes dependencies Hakoniwa takes on under the
hybrid model. The `sbx` rows reuse the command/flag shapes already proven by
`sbxenv` (a pure-shellout wrapper over `sbx`); the `sandboxd` rows are taken
from `sandboxapi/openapi.yaml` v0.21.0.

### 2.1 `sbx` CLI commands (cold path)

| Command (with flags) | Purpose | When |
|---|---|---|
| `sbx daemon status` / `sbx daemon start` | Ensure the daemon is up before any API call | Once per `hako` invocation |
| `sbx login`, `sbx setup` | One-time host auth + first-run setup | Prerequisite (documented, not run by `hako`) |
| `sbx ls --json` | Enumerate existing sandboxes (bootstrap existence check) | Start of `up`/`ps`/`down` |
| `sbx create --name <project>-<agent> [--template <t>] [--cpus <n>] [--memory <m>m] [--kit <ref> ...] <agent> .` | Create a sandbox; resolves credentials + kit refs daemon-side-of-the-CLI | First `up` for each agent |
| `sbx secret set-custom <project>-<agent> [--placeholder <p>] [--host <h>] [--env <e>] --value <v>` | Inject a resolved secret, scoped to one sandbox | `up`, per `secrets[]`/`credentials[]` entry |
| `sbx kit pull <ref>` | Warm/resolve an OCI kit ref (optional; `--kit` on create also resolves) | `up`, when kits are remote |
| `sbx policy set-default <allow-all\|balanced\|deny-all>` | Set the global default policy (one-time; errors if already set) | First `up`, from `defaults.policy.default` |

`sbx run` is **deliberately not used**: it attaches an interactive TTY for a
human. Hakoniwa drives agents non-interactively through the daemon
(`agent/session` / `exec`).

### 2.2 `sandboxd` OpenAPI endpoints (hot path)

| Method + path | operationId | Use in Hakoniwa |
|---|---|---|
| `GET /sandbox` | `listSandboxes` | Discover project sandboxes (filter by `<project>-` name prefix) |
| `GET /sandbox/{name}` | `inspectSandbox` | Dependency gating: `status` (`running`/`stopped`), `ports`, `labels`, `created_at`; `404` ⇒ absent |
| `POST /sandbox` | `createSandbox` | Phase-2 alternative to `sbx create` (needs client-resolved `kit_artifacts`/`credential_values`); `409` on name conflict |
| `POST /sandbox/{name}/start` | `startSandbox` | Bring a stopped sandbox up; returns `SandboxInfo`; `409` ⇒ `port_replay_conflict` |
| `POST /sandbox/{name}/stop` | `stopSandbox` | `down`; returns post-action `SandboxInfo` |
| `DELETE /sandbox/{name}` | `deleteSandbox` | `down`: stops + removes sandbox, network, state |
| `POST /sandbox/{name}/agent/session` | `attachAgentSession` | Drive the agent (initial prompt), stream output (HTTP upgrade) — the event layer taps this |
| `POST /sandbox/{name}/exec` (+ `/exec/attach`) | `exec` / `attachExec` | Readiness probes, payload extraction, ad-hoc commands |
| `GET /sandbox/{name}/files` | `getFile` | Read an emitter's declared output payload |
| `PUT /sandbox/{name}/files` | `putFile` | Stage a subscriber's input payload before start |
| `POST /sandbox/{name}/ports` | `publishPorts` | `reach` wiring + declared `ports[]` |
| `GET /sandbox/{name}/ports` | `listPublishedPorts` | Port convergence (desired vs current) |
| `POST /sandbox/{name}/ports/unpublish` | `unpublishPorts` | Port convergence + `down` |
| `PUT /sandbox/{name}/credentials` | `syncCredentials` | Re-sync `credential_values` / `custom_secrets` to a running sandbox |
| `GET /policy/network/rules?sandbox=<n>&type=network` | `listNetworkPolicyRules` | Read current per-sandbox rules for convergence |
| `POST /policy/network/rules` | `modifyNetworkPolicyRules` | Add/remove/modify allow/deny rules (single endpoint) scoped per sandbox |
| `POST /policy/network/check` | `checkNetworkPolicy` | Pre-flight a target against the effective policy (used by the widening lint) |
| `GET`/`POST /policy/network/setup` | `getNetworkPolicySetupStatus` / `applyNetworkPolicySetup` | Detect/apply a preset on a fresh host |
| `POST /sandbox/{name}/kits` | `addKit` | Attach a mixin kit at runtime (optional) |

### 2.3 Versions and runtime requirements

- **`sandboxapi` OpenAPI: `0.21.0`.** Pin the generated client to this version;
  CI must fail if the daemon advertises an incompatible `api_version`
  (`GET /version` / `GET /daemon/info`).
- **`sbx` CLI**: a build that exposes the commands in §2.1 with the flags shown
  (notably `secret set-custom`, `policy set-default`, `ls --json`,
  `ports --json`). Pin a minimum version in CI rather than a flag-probe at
  runtime.
- **Auth**: a valid Docker Hub session token in the local Docker credential
  store (same as `docker login`). Hakoniwa never handles the token; the daemon
  reads it. *(Verified: `sandboxd` auth middleware.)*
- **Transport**: the daemon Unix socket from `DOCKER_SANDBOXES_API` or
  `sandboxlib.DefaultSocketPath()`.

### 2.4 Gaps → upstream issue candidates

Fields/behaviours Hakoniwa wants to expose that have **no equivalent in
Sandboxes today**. Each is a candidate issue against `docker/sandboxes`.

1. **No custom labels on create.** `SandboxCreateRequest` has no `labels`
   field, yet `SandboxInfo.labels` surfaces `com.docker.sandbox.*` labels.
   Without create-time labels, Hakoniwa cannot stamp `hakoniwa.project` /
   `hakoniwa.agent` and must fall back to the sandbox-name convention for
   ownership detection. **Ask:** accept arbitrary `labels` on create.
2. **No event / pub-sub primitive.** Nothing in `sandboxlib`/`sandboxd` lets
   one sandbox notify another, and there is no lifecycle event stream. All of
   `channels`/`emits`/`subscribes` is client-side, driven by polling `inspect`
   and tailing `agent/session`. **Ask:** a sandbox lifecycle / agent-session
   completion event stream (SSE or watch endpoint).
3. **No inter-sandbox networking.** Sandboxes are isolated; the only
   cross-sandbox path is a published host port over loopback. `reach` cannot
   create a private sandbox-to-sandbox link today. **Ask:** opt-in inter-sandbox
   network attach + name-based service discovery.
4. **Daemon cannot resolve kit refs.** `kits` is rejected; clients must ship
   `kit_artifacts`. This is what forces creation onto the `sbx` cold path.
   **Ask:** optional server-side kit resolution, or a documented, version-stable
   client resolver package.
5. **`sbx create` has no `--json`.** The CLI create path emits no structured
   result, so the created id/ports must be re-fetched via `GET /sandbox/{name}`.
   **Ask:** `sbx create --json`.
6. **Network policy is egress-TCP only.** The engine expresses
   `net:connect:tcp` against domains/CIDRs — no ingress, no inter-sandbox
   policy type — so `reach` traffic cannot be governed by the policy engine.
   **Ask:** an inter-sandbox connectivity policy resource.
7. **No structured agent-session result.** `on_event` needs a reliable
   "finished, here is the output" signal. Session exit is observable via the
   stream/inspect, but there is no structured result, so phase 1 leans on a
   sentinel output file. **Ask:** structured agent-session result (exit status +
   declared output) on inspect.
8. **Plaintext round-trips through the client.** `credential_values` (and
   `secret set-custom --value`) require the orchestrator to hold the resolved
   secret in memory, even briefly. The existing `secretresolver` schemes
   (`op://`, AWS `arn:`) hint at a reference-passing model. **Ask:** a
   reference-passing create path so the orchestrator never materialises
   plaintext.

## 3. YAML schema v0

The per-agent block is a **superset of the `.sbxenv` single-sandbox schema** —
same field names and semantics — lifted into an `agents:` map and wrapped in a
project envelope, with orchestration keys added on top.

```yaml
# hakoniwa.yaml — a multi-agent sandbox application.
# Maps the "collaborative debug" use case (#4): reproducer → fixer → test-writer.

name: bugfix-session          # Project name. Namespaces every sandbox as
                              # "<name>-<agent>" (e.g. bugfix-session-reproducer).
                              # This is the ownership/identity key (see §7).

defaults:                     # NET-NEW. Project-level block merged into every
                              # agent. Merge semantics are defined in §6.
  policy:
    default: balanced         # Global, one-time preset: allow-all | balanced |
                              # deny-all. Applied via `sbx policy set-default`.
                              # Project-level only (it is a host-global setting).
  kits:                       # Base kits added to every agent (union, additive).
    - oci://registry.example.com/kits/base:1.0

channels: [ repro.ready, fix.ready ]   # NET-NEW. The closed set of event channel
                              # names. Every emits/subscribes/depends_on.channel
                              # reference must resolve to a name declared here.

agents:                       # Map of <agent-id> → agent block. Each block is a
                              # .sbxenv config + orchestration keys.

  reproducer:
    agent: claude             # Agent kind. Alias: `command`. One of the built-in
                              # kinds (claude|codex|copilot|docker-agent|droid|
                              # gemini|kiro|opencode|shell) or the name of an
                              # agent-kit listed under `kits`. (.sbxenv field.)
    template: node            # Optional container image override. (.sbxenv field.)
    resources: { cpus: 4, memory: 8192 }   # Optional limits. memory is in MB,
                              # passed to `sbx create --memory <m>m`. (.sbxenv.)
    ports: [ "8080:8080" ]    # Published ports, [HOST_IP:]HOST_PORT:SANDBOX_PORT
                              # [/PROTOCOL]; HOST_PORT required. (.sbxenv field.)
    secrets:                  # Reference-only; never inline. `value` is a host
                              # command whose stdout is the secret (run via
                              # bash -c). Injected with `sbx secret set-custom`.
                              # (.sbxenv field; shape shared with `credentials`.)
      - { value: "gh auth token", env: GH_TOKEN, host: api.github.com }
    emits: [ repro.ready ]    # NET-NEW. Channels this agent publishes to on
                              # completion. Payload = its declared output (§4).

  fixer:
    agent: codex
    depends_on:               # NET-NEW condition `on_event`. Compose-style map of
                              # <agent-id> → { condition, channel? }.
      reproducer: { condition: on_event, channel: repro.ready }
    subscribes: [ repro.ready ]   # NET-NEW. Channels consumed; each fired
                              # payload is staged into this agent before start.
    reach: [ "reproducer:8080" ]   # NET-NEW. "<agent-id>:<port>" services in
                              # other agents' sandboxes this agent may reach (§5).
    policy:                   # Per-agent network policy. allow = widen (opt-in),
                              # deny = restrict (always free). See §6.
      network: { allow: [ "*.github.com" ], deny: [ "*.telemetry.io" ] }
    emits: [ fix.ready ]

  test-writer:
    agent: gemini
    depends_on:
      fixer: { condition: on_event, channel: fix.ready }
    subscribes: [ fix.ready ]
    kits: [ ./kits/test-runner ]   # Per-agent kit. dir | .zip | oci://; a leading
                              # ~ and $VAR/${VAR} are expanded. (.sbxenv field.)
```

### 3.1 Grammar of the net-new keys

**`defaults`** — a partial agent block applied to every agent before the
agent's own block. Mergeable sub-keys in v0: `policy`, `kits`, `secrets`,
`resources`, `template`. `defaults.policy.default` is special: it is the
host-global preset and may *only* appear here, never per agent. Merge rules are
in §6.

**`channels`** — a list of channel-name strings. Names are free-form dotted
identifiers matching `^[a-z0-9]+(\.[a-z0-9]+)*$` (e.g. `repro.ready`). The list
is the *closed vocabulary*: an `emits`, `subscribes`, or `depends_on.channel`
referring to a name not in `channels` is a schema error (§8). Declaring channels
explicitly is what makes the dependency graph statically checkable.

**`emits`** (per agent) — channels the agent publishes to when its session
completes. In v0 the payload of a channel is the agent's **declared output
file**, read by the orchestrator after the session ends (§4). A channel should
have a single emitter in v0; multiple emitters per channel is phase 2.

**`subscribes`** (per agent) — channels the agent consumes. Before the agent
starts, the orchestrator stages each subscribed channel's payload into the
agent's workspace at a known path (§4) so the agent's prompt can read it.
Subscribing does not by itself gate startup — use `depends_on` for ordering.

**`depends_on`** (per agent) — a map of `<agent-id> → { condition, channel? }`.
Conditions in v0:

| condition | meaning | requires |
|---|---|---|
| `created` | the dependency's sandbox exists | — |
| `running` | the dependency's sandbox is `running` | — |
| `completed` | the dependency's agent session has finished | — |
| `on_event` | a named channel has fired (NET-NEW) | `channel:` |

`on_event` is the primary phase-1 condition; it both orders startup and carries
a payload. The dependency graph induced by `depends_on` must be acyclic (§8).

**`reach`** (per agent) — a list of `"<agent-id>:<port>"` targets the agent is
allowed to connect to inside another agent's sandbox. Opt-in only; absent
`reach` means no cross-sandbox connectivity. Feasibility and wiring are in §5.

### 3.2 Compatibility with `.sbxenv`

Hakoniwa intentionally reuses the `.sbxenv` field names so the two formats are
familiar and partly interchangeable:

- An agent block uses the same fields as `.sbxenv`: `name`/`agent`/`command`,
  `template`, `resources{cpus,memory}`, `ports`, `secrets`/`credentials`
  (shape `{ placeholder, value, host, env }`), `kits`, and
  `policy{ default, network{ allow, deny } }`.
- A single-agent Hakoniwa project degenerates to essentially one `.sbxenv`
  block plus the project envelope. A migration path "`.sbxenv` → single-agent
  `hakoniwa.yaml`" is mechanical.
- **Two intentional differences.** (1) `policy.default` is project-level in
  Hakoniwa (under `defaults`), because it is a host-global one-time setting; in
  `.sbxenv` it sits inside the single `policy` block. (2) `name` at the top is
  the *project* name; the per-agent sandbox name is derived as
  `<project>-<agent>`, whereas `.sbxenv`'s `name` is the sandbox name directly.
- The `.sbxenv` security note carries over: the file drives host-side actions
  (policy rules, port publishing, `bash -c` secret commands), so
  `hakoniwa.yaml` is **host-trusted configuration** and should be treated as
  such (reviewed, version-controlled, kept out of an untrusted workspace mount).

## 4. Event model

`channels`/`emits`/`subscribes`/`depends_on: on_event` are entirely net-new:
there is no eventing or inter-sandbox messaging anywhere in Sandboxes today
*(verified)*. The design question is where the channel bus lives.

### 4.1 Options

| Option | Latency | Dependencies / ops | Simplicity | Durability | Multi-host |
|---|---|---|---|---|---|
| **In-process bus** (orchestrator owns a channel registry) | Bounded by the `inspect`/file poll interval (~hundreds of ms) | None — lives in the `hako` process | Highest — no new infra | None (lost if `hako` exits) | No |
| **External broker** (NATS / Redis) | Sub-ms publish/subscribe | A broker to run, secure, and inject into sandboxes | Low — new service + client wiring + auth | Yes (broker persists) | Yes |
| **Native `sandboxd` mechanism** | n/a | n/a | n/a | n/a | n/a |

The native option does not exist (gap #2): there is no lifecycle event stream
and no sandbox-to-sandbox channel. An external broker solves durability and
multi-host but contradicts the phase-1 use cases (#2, #4 are single-host,
one-shot DAGs) and would require injecting broker credentials and network
allowances into every sandbox — a large surface for no phase-1 benefit.

### 4.2 Recommendation: in-process bus (phase 1)

Hakoniwa runs the event bus inside the `hako up` process as an **in-memory
channel registry**, and treats the project as a **one-shot DAG executor**. This
is sufficient for #2 and #4 and adds zero operational dependencies.

### 4.3 Concrete phase-1 implementation

**Build the graph (parse time).** From `channels`, `emits`, `subscribes`, and
`depends_on`, build a DAG whose edges are: `on_event` dependencies, and the
implicit emitter→subscriber edges of each channel. Reject cycles (§8).

**Registry.** A single in-memory map held by the orchestrator:

```
channel -> { fired: bool, emitter: <agent-id>, payloadRef: <host path or blob> }
```

**Payload convention.** Each channel maps to a file path inside the emitter's
workspace:

```
emit:      /<workspace>/.hako/out/<channel>.json   # written by the emitting agent
subscribe: /<workspace>/.hako/in/<channel>.json    # staged by the orchestrator
```

The agent's initial prompt is templated to (a) write its result to the `out`
path for each channel it emits, and (b) read the `in` paths for channels it
subscribes to.

**Emit detection.** The orchestrator drives each agent via
`POST /sandbox/{name}/agent/session` and watches that stream. A channel
*fires* when the agent session completes **and** the declared `out/<channel>.json`
exists. Completion is observed from the session stream closing, corroborated by
`GET /sandbox/{name}` status; the payload is read with
`GET /sandbox/{name}/files?path=/<workspace>/.hako/out/<channel>.json`. The
registry entry is marked `fired` with the payload reference.

> This two-signal rule (session done **and** output present) is the phase-1
> stand-in for the missing structured agent-session result (gap #7). If the
> session ends without producing the file, the channel never fires — surfaced
> as a timeout (below), not a silent hang.

**Subscribe / gating.** An agent with `depends_on: { X: { condition: on_event,
channel: C } }` is not created or started until channel `C` is `fired`. Just
before start, the orchestrator stages every subscribed channel's payload into
the agent via `PUT /sandbox/{name}/files` at `/<workspace>/.hako/in/<channel>.json`.

**Fan-out / fan-in.** A channel may have multiple subscribers (fan-out is
free). An agent may depend on multiple channels (join): it starts only when all
required channels have fired. v0 assumes one emitter per channel; multi-emitter
ordering/merge is phase 2.

**Timeouts and failure.** Each `on_event` edge has a timeout (project-level
default, per-agent override). If the awaited channel has not fired before the
timeout, or the emitter's session exits non-zero, the dependent agents are not
started and `up` fails with a precise message naming the stalled channel and
its expected emitter (§8).

**Scope.** This executor is one-shot and acyclic — ideal for #2 (cross-review:
fan-out to two reviewers, join) and #4 (chained hand-off). Continuous /
long-running event loops (use case #3, an agent that emits repeatedly on a
watch) need a persistent bus and re-trigger semantics and are explicitly
phase 2.

## 5. Inter-sandbox networking (`reach`)

`reach: ["reproducer:8080"]` declares that an agent may connect to a service
listening inside another agent's sandbox. This is the hardest net-new surface
because it runs against the isolation model.

### 5.1 What exists today (verified)

Sandboxes are isolated containers; egress is gated by a MITM proxy and the
network policy engine, which expresses **`net:connect:tcp` against
domains/CIDRs only** — no ingress, no inter-sandbox policy type. There is **no
private sandbox-to-sandbox network**. The single mechanism by which one sandbox
can reach another's service today is a **published host port** over loopback
(`POST /sandbox/{name}/ports` / `sbx ports`).

### 5.2 Options

| Option | Feasible now? | Isolation | Notes |
|---|---|---|---|
| **A. Published host port + loopback** | Yes | Weakest — the port is exposed on the host loopback to anything local | Publish A's `8080` to `127.0.0.1:<hostport>`; inject the address into B |
| **B. Shared private Docker network** | No (not exposed by `sandboxd`) | Strong — direct container-to-container, name-based DNS, nothing on the host | Requires daemon support to attach two sandboxes to one bridge → upstream ask |
| **C. VPN / mesh overlay** | Overkill | Strong but heavy | Only relevant for a future multi-host story; rejected for v0 |

### 5.3 Recommendation

**Design now; implement the minimum in phase 1, the clean version in phase 2.**

- **Phase 1 (MVP, option A).** Because use case #4 needs `reach`, wire it with
  host-port publishing: for `reach: ["reproducer:8080"]`, publish the
  reproducer's `8080` (via `publishPorts`), then inject the reachable address
  into the consumer as an environment variable, e.g.
  `HAKO_REACH_REPRODUCER_8080=<host-loopback>:<hostport>`. The exact hostname a
  sandbox uses to address the host (`host.docker.internal` vs the bridge
  gateway IP) must be confirmed against the sandbox network setup before
  implementation — **flagged as a to-verify**, not assumed here.
- **Phase 2 (target, option B).** A private bridge joining only the named
  sandboxes, with name-based DNS (`reproducer:8080` resolves directly), no host
  exposure. This needs a new `sandboxd` capability (gaps #3 and #6) and is the
  preferred end state.

### 5.4 Security posture

`reach` is **opt-in per consumer and per port**: absent `reach`, an agent gets
no cross-sandbox connectivity. The orchestrator publishes only the exact ports
named in some agent's `reach`, and pairs that with deny-by-default egress (§6)
so a reachable consumer still cannot talk to anything else. Under the phase-1
MVP the published port is visible on the host loopback to other local
processes; this is an accepted, documented limitation until option B lands.

## 6. Composition rules: project ↔ agent

How `defaults` (project) combines with each agent block, for the three
security-sensitive surfaces. The guiding principle is **least privilege by
default**: composition may tighten an agent's footprint freely, but loosening
it requires an explicit opt-in.

| Surface | Project (`defaults`) | Agent | Merge | Loosening allowed? |
|---|---|---|---|---|
| Kits | base kit set | extra kits | **additive union** (dedup) | n/a (agent can only add) |
| Secrets | named library | opt-in by name | **no implicit inheritance** | agent must list each secret it gets |
| Network policy | preset + project allow/deny | agent allow/deny | preset → project rules → agent `deny` → agent `allow` | **only with opt-in** (widening lint) |

### 6.1 Kits — additive

An agent's effective kits are `defaults.kits ∪ agent.kits` (union, de-duplicated
by reference). An agent can add kits but cannot remove a project base kit in v0
(no subtraction operator). Kit-install policy — `allowed_sources` /
`allow_local`, enforced by `sbx` at resolution time — is **project-level only**;
an agent cannot widen the set of sources it may pull kits from.

### 6.2 Secrets — strict per-agent scoping, no inheritance

Secrets are **not inherited**. `defaults` may declare a *library* of named
secret definitions, but an agent receives a secret only if it lists that name
in its own `secrets`/`credentials`. This enforces the strict per-agent scoping
requirement and matches the underlying mechanism: `sbx secret set-custom` is
scoped to a single sandbox, so a secret is materialised only on the sandboxes
that opted in. The safe default is therefore "no secret crosses an agent
boundary unless that agent explicitly asked for it."

### 6.3 Network policy — restrict freely, widen by opt-in

The effective policy for an agent's sandbox is computed in order:

1. **Preset** — `defaults.policy.default` (`allow-all` | `balanced` |
   `deny-all`), applied host-globally once via `sbx policy set-default`.
2. **Project rules** — any project-wide `allow`/`deny` (a future
   `defaults.policy.network` block), applied to every agent's sandbox.
3. **Agent `deny`** — always applied. Denies are unconditionally safe.
4. **Agent `allow`** — applied only if permitted by the widening lint.

This ordering is sound because the underlying engine resolves **deny over
allow** *(verified)*: adding a `deny` can only tighten, so it is always allowed;
adding an `allow` can only loosen, so it is gated.

**The widening lint (net-new, orchestrator-side).** `sbx` itself accepts any
`allow` rule — it does not enforce a project ceiling — so the ceiling must be a
Hakoniwa validation step. Rule: an agent `allow` entry that is **not already
permitted by the project baseline** (the preset's implied set plus project
`allow` rules) is a **validation error**, unless widening is explicitly enabled.
The opt-in knob lives at project level:

```yaml
defaults:
  policy:
    default: deny-all
    allow_widening: false      # default. When false, an agent may not add an
                               # allow beyond the project baseline; doing so is
                               # a hard error. Set true to let agents broaden.
```

With `allow_widening: false` (the safe default), the cross-review use case (#2)
gets exactly the property we want: a reviewer agent can carve its egress *down*
to nothing, but cannot quietly open a path the project did not sanction. When a
project legitimately needs a single agent to reach more, it flips the knob and
the widening becomes visible in the file and in `hako plan`. Before applying an
agent `allow`, the orchestrator may pre-flight it with
`POST /policy/network/check` to report the effective decision.

Convergence is then applied per sandbox via `POST /policy/network/rules`
(scoped with `?sandbox=<project>-<agent>`), reconciling desired vs current
rules — and, mirroring `sbxenv`'s discipline, touching only the rules Hakoniwa
itself created, never default/blueprint/remote-managed rules.

## 7. Idempotence and lifecycle

### 7.1 Naming and ownership

Each agent maps to a sandbox named **`<project>-<agent>`** (e.g.
`bugfix-session-reproducer`). The name is the identity key, exactly as `sbxenv`
uses it for find-or-create. Project resources are discovered by listing
sandboxes (`GET /sandbox`) and filtering on the `<project>-` prefix.

> **Ownership caveat (gap #1).** Until `createSandbox` accepts custom labels,
> the name prefix is the *only* ownership signal — Hakoniwa cannot distinguish
> "a sandbox I created" from "a sandbox someone else named `bugfix-session-x`."
> v0 therefore treats the prefix as authoritative and **warns** on a
> pre-existing same-named sandbox it did not create this run. Once upstream
> adds labels, switch ownership detection to a `hakoniwa.project=<name>` label
> (read back from `SandboxInfo.labels`, which already exists) and keep the name
> convention only for human readability.

### 7.2 `hako up` — find-or-create + converge

`up` is idempotent. It reconciles desired state (the file) against current state
(the daemon), creating what is missing and converging what exists:

```
hako up [-f hakoniwa.yaml] [--rerun <agent>...]
```

1. Load + validate the file (§8), then ensure the daemon is up
   (`sbx daemon status` / `start`).
2. Build the DAG (§4) and walk agents in dependency order, respecting
   `on_event` gates.
3. For each agent, when its gates are satisfied:
   - **Exists?** `GET /sandbox/<project>-<agent>` → `200` (reuse) vs `404`
     (create). If absent, `sbx create --name <project>-<agent> … <agent> .`
     (cold path: resolves credentials + kits).
   - **Converge infrastructure** (declarative, desired-vs-current diffs, same
     discipline as `sbxenv`): secrets (`sbx secret set-custom`), network policy
     (`POST /policy/network/rules`), published ports + `reach`
     (`publishPorts` / `unpublishPorts`).
   - **Start** if `stopped` (`POST /sandbox/{name}/start`).
   - **Drive** the agent: `POST /sandbox/{name}/agent/session` with the
     templated prompt; stage subscribed payloads first; on completion detect
     emits and fire channels (§4).

**Re-`up` semantics.** Re-running `up` reuses existing sandboxes (find-or-create
by name) and re-converges infrastructure (ports/policy/secrets) idempotently. It
does **not** automatically replay agent sessions that already completed — a
one-shot DAG that finished stays finished. To re-drive a specific agent (and the
dependents it feeds), use `hako up --rerun <agent>`, which clears that agent's
emitted channels and re-walks the affected sub-graph. This keeps re-`up` safe
and cheap by default while making replay explicit.

### 7.3 `hako down`

```
hako down [-f hakoniwa.yaml]
```

Stops and removes every `<project>-*` sandbox (`DELETE /sandbox/{name}`, which
also tears down the sandbox network and state), unpublishes the project's
ports, and removes **only the policy rules Hakoniwa created** (tracked the way
`sbxenv` tracks its `local:<uuid>` rules) — never default, blueprint, or
remote-managed rules. `down` is idempotent: absent sandboxes are skipped.

### 7.4 `hako plan`

Dry run. Parses + validates the file and computes the diff against current
state **without mutating anything**: which sandboxes would be created vs reused,
which ports/policy/secrets would converge, the resolved DAG and gate order, and
any widening-lint or ownership warnings. `plan` is the safe way to preview a
run and the natural output for review.

### 7.5 `hako logs` and `hako ps`

- `hako logs [<agent>] [--follow]` — stream agent output by tailing
  `POST /sandbox/{name}/agent/session` (and `exec` logs where relevant), per
  agent or for the whole project, annotated with channel-fired events.
- `hako ps` — list the project's sandboxes from `GET /sandbox` (prefix-filtered)
  as a table: agent, sandbox name, status (`running`/`stopped`), published
  ports, and last channel emitted. This is the project-scoped analogue of
  `sbx ls`.

## 8. Validation

Validation runs in two layers with a hard boundary between them. Layer 1 needs
nothing but the file; layer 2 talks to the system. `plan` and `up` both run
layer 1 to completion first (fail fast, report **all** errors), then layer 2.

### 8.1 Layer 1 — schema validation (static, no system access)

Pure function of the file. Checks:

- **Well-formedness**: valid YAML; unknown keys rejected (strict decoding).
- **Required fields**: non-empty `agents`; each agent has an `agent`/`command`
  resolving to a known kind or an agent-kit name; `name` present (or defaulted).
- **Enums / formats**: `policy.default` ∈ {`allow-all`,`balanced`,`deny-all`};
  port specs match `[HOST_IP:]HOST_PORT:SANDBOX_PORT[/PROTO]` with `HOST_PORT`
  required (reuse the `sbxenv` parser); `resources.cpus`/`memory` ≥ 0; channel
  names match `^[a-z0-9]+(\.[a-z0-9]+)*$`.
- **Reference integrity**: every `emits`/`subscribes`/`depends_on.channel`
  resolves to a declared `channels` entry; every `depends_on` key is a defined
  agent; `on_event` carries a `channel`; every `reach` target `"<agent>:<port>"`
  names a defined agent and a port that agent actually publishes in its `ports`.
- **Graph**: the `depends_on` graph is acyclic.
- **Widening lint** (§6): an agent `allow` beyond the project baseline with
  `allow_widening: false` is an error.

All layer-1 problems are collected and reported together, each anchored to its
YAML location using `gopkg.in/yaml.v3` node line/column.

### 8.2 Layer 2 — system validation (against live state)

Requires the daemon and host; run after layer 1 passes:

- **Daemon reachable** — `sbx daemon status` / socket ping; remediation
  `sbx daemon start`.
- **Auth present** — a Docker Hub token in the credential store; remediation
  `sbx login`.
- **Resolvability** — agent templates / kit refs pullable under `pull_policy`;
  each secret `value` command runs with exit 0 and non-empty stdout.
- **Name collisions** — a `<project>-<agent>` sandbox that exists but was not
  created by this project (ownership caveat, §7.1): **warning** in v0.
- **Port availability** — surfaced from `publishPorts` / a `409`
  `port_replay_conflict` on start.

Secret resolution differs from `sbxenv` here: `sbxenv` silently skips a secret
whose command fails, but for `hako up` a secret an agent explicitly declared is
treated as **required** — a failed/empty command is an error (with the failing
command named), not a silent skip. A secret marked optional may downgrade to a
warning.

### 8.3 Error format

Text to stderr, machine-readable JSON under `--json`. Each diagnostic carries a
severity (`error` aborts; `warning` proceeds), a dotted path into the document,
the source location for layer-1 issues, and a remediation hint for layer-2
issues.

```
# Layer 1 (schema)
hako: hakoniwa.yaml:21:7: agents.fixer.depends_on.reproducer:
  channel "repro.redy" is not declared in top-level channels
  [repro.ready, fix.ready]

hako: hakoniwa.yaml:30:5: agents.fixer.reach[0]:
  "reproducer:9090" — agent "reproducer" does not publish port 9090
  (declared ports: [8080:8080])

hako: validation failed: 2 errors, 0 warnings

# Layer 2 (system)
hako: agents.reproducer.secrets[0]: value command failed (exit 1):
  `gh auth token` — run `gh auth login`, or mark the secret optional

hako: warning: sandbox "bugfix-session-reproducer" already exists and was not
  created by this project; `up` will reuse it (see ownership caveat)
```

The `409` conflict from `createSandbox`/`startSandbox` is mapped to an
actionable message rather than surfaced raw: a name conflict becomes "reusing
existing sandbox" (expected in re-`up`), and a `port_replay_conflict` names the
contended host port and the agent that wants it.
