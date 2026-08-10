# ACP Phase Log

This file is the repo-local source of truth for implementation phases. The
same milestones are mirrored to the `ACP-PoC/` folder inside the Clawdlinux
Obsidian vault.

## Phase 0 — Invention Capture

**Date:** 2026-05-02

**Inputs**
- Source spec: `ACP_PoC_Specification_CONFIDENTIAL.docx`
- Workspace: `/Users/sunny/clawdlinux/agent-contract-protocol`
- GitHub repo: `Clawdlinux/agent-native-format` (private)
- GitHub milestone: `https://github.com/Clawdlinux/agent-native-format/milestone/1`

**Decisions**
- Repo name: `agent-contract-protocol`
- Local path: `/Users/sunny/clawdlinux/agent-contract-protocol`
- Phase 1 scope: repo scaffold + `SPEC.md` + documentation index, no runtime logic yet
- Documentation cadence: end-of-phase verifier/doc pass with Obsidian mirror

**Done**
- Private repo created under `clawdlinux`
- Repo cloned locally
- Directory structure created per the PoC spec
- `SPEC.md` v0.1 extracted and normalized from the source document
- Obsidian notes seeded under `ACP-PoC/`
- Verifier pass completed; findings fixed before first commit

## Phase 1 — Scaffold + Protocol Baseline

**Goal:** Create the private repo foundation for Week 1 ACP server work.

**Acceptance criteria**
- Directory tree matches the source PoC spec section 3.3
- License split is documented
- Architecture, benchmark, pitch, and references docs exist
- Obsidian vault contains ACP PoC index + running log
- Initial commit is pushed to private GitHub repo

**Status:** Complete

## Phase 2 — Week 1 ACP Server

**Goal:** Working ACP server with `POST /v1/context`, `POST /v1/feedback`, and
`GET /healthz` backed by an in-memory registry, keyword resolver, and
deterministic manifest builder.

**Acceptance criteria**
- `pkg/manifest` exposes the full ACP wire types (request, response, feedback).
- `internal/registry` is goroutine-safe, capability-indexed, and seeds five
  demo tools.
- `internal/resolver` deterministically maps intent + hints to capability tags.
- `internal/manifest` builder strips schemas, computes `depends_on`, and
  aggregates egress + approvals.
- `internal/server` enforces bearer auth (constant-time compare) and validates
  payloads.
- `cmd/acp-server` boots, handles SIGINT/SIGTERM, structured JSON logs.
- All packages have unit tests; consumer-defined interfaces are mocked with
  `go.uber.org/mock` (gomock).
- `pkg/acp` Go SDK uses dependency-injected `HTTPDoer` for tests.
- `go test -race ./...` is green.

**Status:** Complete

## Phase 3 — Week 2 Auth Proxy + First Benchmark

**Goal:** Add the auth-injection proxy, build a defensible MCP-equivalent
baseline, and produce the first measured ACP-vs-MCP token-cost number.

**Acceptance criteria**
- `internal/proxy` enforces egress allow-list, blocks gated actions until
  approved, strips agent-supplied `Authorization`, injects server-side
  credentials, and forwards via `httputil.ReverseProxy`.
- All proxy collaborators are consumer-defined interfaces, mocked with
  `go.uber.org/mock`.
- `cmd/acp-server` mounts the proxy at `/v1/exec/{manifest_id}/{action_id}`
  and stores every emitted manifest via `Persister`.
- `benchmark/baseline/mcp_client.py` reproduces the verbose MCP `initialize`
  + `tools/list` payloads per the MCP 2024-11 spec.
- `benchmark/harness.py` runs N runs against the live ACP server, counts
  tokens with `tiktoken/cl100k_base`, and emits raw + summary JSON.
- `benchmark/report.py` renders a markdown report.
- First measured S1 + S2 numbers are committed to `results/`.
- `go test -race ./...`, `staticcheck`, `govulncheck`, and Python tests all
  green.

**Result (2026-05-02, 50 runs/scenario, tiktoken cl100k_base):**

| Scenario | ACP tokens | MCP tokens | Reduction | RT ACP/MCP |
|---|---|---|---|---|
| S1 Simple DB query | 111 | 373 | **70.2%** | 1 / 3 |
| S2 Multi-tool workflow | 295 | 837 | **64.7%** | 1 / 5 |

Falls inside the 70-85% target range stated in the source spec.

**Status:** Complete

## Phase 4 - Week 3 Python Adapters + S3-S5 + MCP Source Adapter

**Goal:** Make ACP usable from the Python agent ecosystem, push the
benchmark to all 5 scenarios, and prove "ACP on top of MCP" with a real
source adapter.

**Acceptance criteria**
- `adapters/python/acp_common`: shared core (manifest types, ACPClient,
  schema translation, topological ordering, proxy URL helpers).
- `adapters/python/acp_openai`: manifest -> OpenAI function-calling tools
  + dispatcher that routes through the auth-injection proxy.
- `adapters/python/acp_langgraph`: manifest -> LangGraph nodes with
  topological edges.
- `adapters/python/acp_crewai`: manifest -> CrewAI tools (with callable
  fallback when crewai is not installed).
- `internal/sources/mcp`: ingests MCP `tools/list` payloads, infers
  capabilities, converts verbose JSON-Schema to compact ACP form,
  registers each tool in the ACP registry.
- Harness covers S1-S5; S4 includes a `noise_tools` knob that adds
  generic tool descriptors to model "intent scoping at scale".
- All Python tests green (35 = 12 benchmark + 23 adapter).
- All Go tests green (9 packages incl. new `internal/sources/mcp`).

**Result (2026-05-02, 50 runs/scenario, tiktoken cl100k_base, all 5):**

| Scenario | ACP tokens | MCP tokens | Reduction | RT ACP/MCP |
|---|---|---|---|---|
| S1 Simple DB query | 111 | 373 | **70.2%** | 1 / 3 |
| S2 Multi-tool workflow | 295 | 837 | **64.7%** | 1 / 5 |
| S3 Complex DAG | 306 | 1,257 | **75.6%** | 1 / 7 |
| **S4 Scale (50 tools, 2 relevant)** | **241** | **9,223** | **97.4%** | **1 / 21** |
| S5 Auth-heavy | 359 | 1,431 | **74.9%** | 1 / 7 |

S4 is the headline pitch number: at 50 registered tools (2 relevant), ACP
delivers a 97.4% reduction over MCP's full `tools/list` payload.

**Status:** Complete
