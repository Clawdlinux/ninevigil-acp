# Agent Contract Protocol (ACP) - v0.2 DRAFT

> **Status:** DRAFT - June 4, 2026
> **Editors:** Shreyansh Sancheti (Clawdlinux)
> **License:** CC BY 4.0 (spec only; see [LICENSE](./LICENSE) for runtime)
> **Tagline:** Governed execution contracts for autonomous agents.

---

## 1. Abstract

The **Agent Contract Protocol (ACP)** is a wire protocol between an autonomous
agent (or agent runtime) and a contract-resolution service that returns a
complete, intent-scoped **Execution Contract**. The contract declares the
exact endpoints, compact schemas, identity binding, authentication injection,
execution dependencies, policy boundaries, and audit hooks required to satisfy
the agent's stated intent.

ACP is a **governed execution layer that sits on top of MCP** and other tool
sources (REST APIs, gRPC services, Kubernetes APIs, CLIs). MCP answers "what
tools exist?"; ACP answers "what execution is allowed for this agent, right
now?". ACP servers consume MCP `tools/list` payloads (see
[§10](#10-relationship-to-mcp)) and other catalogs, then emit contracts that
any agent runtime can execute through a policy-enforcing proxy.

## 2. Motivation

MCP was designed for tool discovery. It does that job well. Claude Code Tool
Search and semantic retrieval are also making large tool catalogs cheaper to
query. Tool discovery and schema cost are becoming platform problems.

Production autonomy still needs a separate execution contract. Before an agent
touches a real system, infrastructure must answer:

| Failure mode | Operational cost |
|---|---|
| Unbound identity | Actions cannot be tied to a principal and credential alias. |
| Policy drift | Egress, approval, TTL, and rate limits are enforced ad hoc. |
| Credential exposure | Agents may see or relay credentials meant for upstream systems. |
| Ordering inferred | Agents spend tokens and make mistakes reasoning about dependencies. |
| Weak audit | Tool calls are not attached to a signed, replayable unit of intent. |

ACP does not replace MCP. MCP servers stay where they are. ACP consumes their
catalogs, scopes capabilities by intent, binds execution to identity and policy,
injects credentials at the proxy boundary, pre-computes ordering, and emits a
single contract the proxy can enforce.

Token minimization remains useful and measured. It is a side effect of scoped
contracts, not the central claim.

Cited references in [docs/references.md](./docs/references.md). See also
[docs/positioning.md](./docs/positioning.md) for the full “ACP on top of
MCP” framing.

## 3. Design Principles

1. **The infrastructure computes the contract, not the agent.**
2. **One intent, one contract.** Discovery, schema, identity, policy, auth,
  and ordering arrive together.
3. **Policy is enforced at the boundary.** The proxy checks the contract before
  any upstream call.
4. **Token-minimal by construction.** Schemas are field-name + type only.
5. **Auth never enters the agent's context window.** Injection happens at
   the proxy boundary.
6. **Execution is declared, not inferred.** `depends_on` is explicit.
7. **Boundaries are part of the contract.** Egress, budgets, approvals, and
   audit level are returned with the manifest.
8. **Every result is an audit and feedback signal.** Feedback endpoint is mandatory.
9. **Deferred resolution is first-class.** Agents MAY operate without
   upfront intent. The server starts broad and narrows as observations
   accumulate. See §4.8.

## 4. Protocol

### 4.1 Endpoint

```
POST /v1/context
Content-Type: application/json
Authorization: Bearer <agent-identity-token>
```

### 4.2 Request

```json
{
  "intent": "query customer data, generate report, email to team",
  "agent_id": "analytics-agent-01",
  "capabilities": ["sql", "template", "email"],
  "constraints": {
    "max_tokens": 50000,
    "timeout": "120s",
    "output_format": "minimal"
  }
}
```

| Field | Type | Required | Notes |
|---|---|---|---|
| `intent` | string | yes | Natural-language statement of what the agent wants to accomplish. |
| `agent_id` | string | yes | Stable identity. Used for policy + audit. |
| `capabilities` | string[] | no | Optional hints the resolver may use to short-circuit intent parsing. |
| `constraints.max_tokens` | int | no | Token budget the contract must respect. |
| `constraints.timeout` | duration | no | Wall-clock budget for the entire DAG. |
| `constraints.output_format` | enum | no | `minimal` \| `verbose`. Default `minimal`. |

### 4.3 Response - Execution Contract

The v0.1 JSON field names keep `manifest_id` for compatibility. In v0.2 and
later documents, this object is an Execution Contract.

```json
{
  "manifest_id": "m-a7f3b2",
  "version": "acp/v1",
  "ttl": "300s",
  "actions": [
    {
      "id": "a1",
      "type": "http",
      "endpoint": "grpc://db-proxy.svc:50051/query",
      "method": "POST",
      "schema": { "sql": "string", "limit": "int?" },
      "auth": "pre-injected",
      "timeout": "30s"
    },
    {
      "id": "a2",
      "type": "template",
      "endpoint": "http://template-svc.svc:8080/render",
      "method": "POST",
      "schema": { "template_id": "string", "data": "json" },
      "auth": "pre-injected",
      "depends_on": ["a1"]
    },
    {
      "id": "a3",
      "type": "http",
      "endpoint": "https://email-gw.svc:443/send",
      "method": "POST",
      "schema": {
        "to": "string[]",
        "subject": "string",
        "body": "string",
        "attachment_ref": "string?"
      },
      "auth": "pre-injected",
      "depends_on": ["a2"]
    }
  ],
  "boundaries": {
    "egress": ["db-proxy.svc", "template-svc.svc", "email-gw.svc"],
    "max_tokens_per_action": 15000,
    "require_approval": ["a3"],
    "audit_level": "full"
  },
  "feedback_endpoint": "http://acp-feedback.svc.cluster.local/v1/feedback"
}
```

### 4.4 Schema mini-language

Field types are declared as compact strings:

| Token | Meaning |
|---|---|
| `string`, `int`, `float`, `bool`, `json`, `bytes` | Scalar types |
| `T?` | Optional |
| `T[]` | Array of `T` |
| `enum:a\|b\|c` | Enum |
| `ref:<id>` | Opaque reference (e.g. attachment handle) |

Rationale: a 6,000-token JSON Schema collapses to ~80 tokens.

### 4.5 Action object

| Field | Required | Notes |
|---|---|---|
| `id` | yes | Stable within manifest. Referenced by `depends_on`. |
| `type` | yes | `http` \| `grpc` \| `template` \| `tool` (extensible). |
| `endpoint` | yes | URI reachable via the auth proxy. |
| `method` | yes (http) | HTTP verb. |
| `schema` | yes | Compact field map (see 4.4). |
| `auth` | yes | `pre-injected` \| `none`. Never raw credentials. |
| `timeout` | no | Per-action wall-clock budget. |
| `depends_on` | no | Array of action `id`s that must complete first. |

### 4.6 Boundaries

| Field | Notes |
|---|---|
| `egress` | Allowed destination hosts. Enforced at the proxy. |
| `max_tokens_per_action` | Server-side ceiling on payload size. |
| `require_approval` | Action ids that require human-in-the-loop approval. |
| `audit_level` | `none` \| `summary` \| `full`. |

### 4.7 Feedback

After each action the agent SHOULD POST:

```
POST /v1/feedback
{
  "manifest_id": "m-a7f3b2",
  "action_id": "a1",
  "outcome": "success" | "error" | "skipped",
  "latency_ms": 124,
  "tokens_in": 312,
  "tokens_out": 88,
  "error": null
}
```

Feedback feeds the optimizer. Compliance is encouraged but not required for
v0.1.

### 4.8 Deferred Intent Mode

ACP v0.1 assumes intent is known when the client calls `POST /v1/context`.
In interactive environments — IDE copilots, chat interfaces, REPL-style
agents — intent does not exist at session start. It emerges over time as
the user works.

**Deferred Intent Mode** is a first-class protocol extension that lets an
ACP-backed tool surface start broad and narrow progressively as
observations accumulate.

#### 4.8.1 Lifecycle

```
Phase 1: Cold Start (0 observations)
  └─ Resolver returns ALL registered capabilities.
  └─ Client sees the full tool surface (schema-compacted).

Phase 2: Warming (1..N-1 observations, below threshold)
  └─ Resolver still returns all capabilities.
  └─ Server records tool-call observations silently.

Phase 3: Narrowed (≥N observations)
  └─ Resolver returns only observed capability domains.
  └─ Server emits a list-change notification.
  └─ Client re-fetches and sees a smaller tool surface.
```

The narrow threshold `N` is server-configured (default: 3).

#### 4.8.2 Observation signal

The server records each `tools/call` invocation as an observation. The
observed tool's capability tags are added to a sliding window of size `W`
(default: 10). When the window contains `≥N` entries, the resolver
switches from "return all" to "return observed domains only."

#### 4.8.3 Re-broadening

If the client explicitly provides capability hints (via the `capabilities`
field in a `POST /v1/context` request, or via MCP tool-call arguments),
those hints are always honored and merged with observed domains. This
prevents the narrowed surface from becoming a dead end.

A `Reset` operation clears all observations and returns the resolver to
cold-start phase. Implementations SHOULD expose reset as either:
- A `POST /v1/context` with `intent: "*"` (wildcard).
- A protocol-specific mechanism (e.g. MCP server restart).

#### 4.8.4 MCP bridge integration

When ACP operates as an MCP server (the "ACP-MCP bridge"):
- The bridge declares `"tools": {"listChanged": true}` during MCP
  `initialize`.
- On `tools/list`: returns the resolver's current scoped tool set.
- On `tools/call`: proxies to the downstream MCP server and records
  capability observations.
- When narrowing changes the tool surface, emits
  `notifications/tools/list_changed` so the MCP client re-fetches.
- Schema compaction (§4.4) is applied unconditionally to all tools.
- Auth is NOT intercepted; it stays with each downstream MCP server
  process. This differs from the standard ACP auth proxy (§4.6) and is
  by design: IDE MCP clients manage credentials at the transport layer.

#### 4.8.5 Conformance

A conforming deferred-mode **server** MUST:
- Start with the full capability set when no observations exist.
- Narrow only after a configurable threshold of observations.
- Always honor explicit capability hints from the client.
- Provide a mechanism to reset to cold-start phase.

A conforming deferred-mode **client** SHOULD:
- Re-fetch the tool surface when notified of changes.
- Not cache tool definitions beyond the server's TTL.

## 5. Conformance

A conforming **client** MUST:
- Send exactly one `POST /v1/context` per intent.
- Treat `auth: pre-injected` as opaque; never log credentials.
- Honor `depends_on` ordering.
- Honor `boundaries.egress` (refuse calls to other hosts).

A conforming **server** MUST:
- Return a contract whose total schema payload is no larger than the
  equivalent MCP `tools/list` payload would be.
- Inject auth at the proxy boundary.
- Expire contracts at `ttl`.

## 6. Versioning

`version` is `acp/<major>`. Breaking changes bump the major. v0.x is
pre-stable.

## 7. Security Considerations

- Identity tokens MUST be short-lived (≤1h recommended).
- Execution Contracts MUST NOT contain secret material.
- Egress allow-lists MUST be enforced at the proxy, not by the agent.
- `require_approval` actions MUST block at the proxy until an out-of-band
  approval is recorded.

## 8. References

See [docs/references.md](./docs/references.md) for the full citation list
(Perplexity, Apideck, Eckstein, Scalekit, arXiv 2602.14878, Cloudflare
Code Mode, AgentSpec, A2A, Oracle Open Agent Spec).

## 9. Changelog

- **v0.1 (2026-05-02)** - Initial draft extracted from
  `ACP_PoC_Specification_CONFIDENTIAL.docx`.
- **v0.1.1 (2026-05-03)** - Repositioned from "successor to MCP" to
  "intent-resolution layer on top of MCP and other tool sources". No wire
  format changes. Added §10.
- **v0.2.0 (2026-05-16)** - Added §4.8 Deferred Intent Mode for
  interactive environments (IDE copilots, chat agents). Added Design
  Principle #8. Reference implementation: `internal/resolver/deferred.go`,
  `internal/bridge/`, `cmd/acp-bridge/`.
- **v0.2.1 (2026-06-04)** - Reframed ACP as Agent Contract Protocol. The
  v0.1 `ExecutionManifest` wire type remains for compatibility, but the
  protocol now defines the object as an Execution Contract.

## 10. Relationship to MCP

ACP **does not replace MCP**. ACP is a layer that consumes MCP (and other
tool sources) and emits intent-scoped execution contracts.

```
agent ── POST /v1/context ──> ACP server ──┬── reads MCP tools/list
                                           ├── reads REST/gRPC catalogs
                                           └── reads Kubernetes APIs
      <── ExecutionContract ──
```

A conforming ACP server SHOULD provide an MCP source adapter. The reference
implementation in this repository ships one at
`internal/sources/mcp.Importer` (Go). The adapter:

1. Issues `GET <mcp_base>/tools/list` against any MCP-compliant server.
2. Infers ACP capability tags from each tool's name, plus optional
   source-level extras.
3. Compacts the verbose JSON-Schema `inputSchema` into the ACP
   mini-language defined in §4.4 (`string`, `int?`, `string[]`,
   `enum:a|b|c`, etc.).
4. Registers each tool in the ACP registry under
   `<source_name>.<tool_name>`.

The compaction step still produces the token reduction headlined in
[docs/pitch-deck-data.md](./docs/pitch-deck-data.md). The MCP server itself
is unchanged; the agent talks to ACP so execution can be scoped, ordered,
identity-bound, and audited before the proxy calls the upstream tool.

### Conformance for an MCP-source adapter

An adapter MUST:

- Translate MCP `tools/list` descriptors into ACP `Tool` entries without
  loss of *executable* meaning (endpoint, method, required fields).
- Strip MCP `description`, `examples`, and JSON-Schema metadata that does
  not affect execution.
- Preserve MCP authentication mode by mapping it to `auth: pre-injected` on
  the ACP side and routing actual credentials through the ACP auth proxy.
- Set `egress` allow-list to the MCP server's host.

An adapter SHOULD:

- Tag each imported tool with at least one capability derived from the
  source name (e.g. `github`, `slack`).
- Honor MCP `notifications/tools/list_changed` to refresh the ACP
  registry.

See also [docs/positioning.md](./docs/positioning.md) for the strategic
rationale and ecosystem implications of this layering.
