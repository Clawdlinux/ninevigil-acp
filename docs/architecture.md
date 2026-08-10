# ACP Architecture

ACP shifts execution governance from the agent runtime to the Clawdlinux
control plane. **ACP sits on top of MCP** and other tool sources; see
[positioning.md](./positioning.md) for the strategic framing and
[SPEC.md §10](../SPEC.md#10-relationship-to-mcp) for the wire-level details.

```
agent ── POST /v1/context ──> ACP server ──┬── reads MCP `tools/list`
                                           ├── reads REST/gRPC catalogs
                                           └── reads Kubernetes APIs
      <── ExecutionContract ──
```

## Components

| Component | Responsibility | Language |
|---|---|---|
| ACP Server | Receives intent, resolves capabilities, computes contracts | Go |
| Tool Registry | Stores tools, compact schemas, auth configs, capability tags | Go |
| Intent Resolver | Maps natural language intent to required capabilities | Go first, embeddings later |
| Contract Builder | Selects relevant actions, strips schemas, computes ordering | Go |
| Auth Proxy | Injects credentials at the network boundary | Go / Envoy |
| **MCP Source Adapter** | Ingests MCP `tools/list` and registers each tool with compact ACP schemas | Go (`internal/sources/mcp`) |
| Benchmark Harness | Runs ACP vs MCP, records tokens, latency, success rate | Python |
| MCP Baseline | Reference MCP `tools/list` payload reproducer for benchmarks | Python |
| Agent Adapters | Consume ACP contracts from LangGraph, CrewAI, OpenAI flows | Python |

## Request path

1. Agent sends `POST /v1/context` with `intent`, `agent_id`, optional
   capabilities, and constraints.
2. ACP Server authenticates the agent identity token.
3. Intent Resolver maps intent to capability tags.
4. Tool Registry returns candidate tools (including those imported from MCP
   sources via the MCP source adapter).
5. Contract Builder strips schemas, orders actions, attaches boundaries.
6. Auth Proxy prepares per-action credential injection without exposing
   secrets to the agent.
7. ACP Server returns one Execution Contract.
8. Agent executes actions in contract order via the proxy and reports
   feedback.

## Tool sources

ACP is source-agnostic. The reference implementation ships an MCP source
adapter; future adapters can ingest:

- REST APIs with OpenAPI 3.x specs
- gRPC services with reflection enabled
- Kubernetes APIs (`agentic-operator-core` integration)

Each source converts its native catalog into ACP `Tool` entries and
registers them. The agent never sees the source's native protocol; it only
sees the compact ACP contract.

## Deployment modes

| Mode | Description | Target |
|---|---|---|
| Local PoC | Docker Compose with mock services and benchmark harness | Weeks 1-2 |
| Cluster PoC | K8s manifests for ACP server + proxy + demo tool services | Weeks 2-3 |
| Operator deployment | Packaged through agentic-operator for regulated clusters | Post-PoC |

## Security model

- Agents authenticate to ACP with short-lived identity tokens.
- Execution Contracts never contain raw credentials.
- Auth injection happens at the proxy boundary.
- Egress allow-lists are enforced by the proxy.
- Human approval gates are declared in `boundaries.require_approval` and
  enforced out of band.
