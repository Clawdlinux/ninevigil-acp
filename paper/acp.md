# Agent Native Format: Token-Efficient Execution Contracts for Autonomous Agent Tool Use

**Shreyansh Sancheti**
*Clawdlinux*
`github.com/Clawdlinux`

---

## Abstract

Production agent systems built on the Model Context Protocol (MCP)
spend a large fraction of their context window on tool discovery and
verbose JSON-Schema descriptors before performing any user task.
We present the execution-contract layer of **Agent Native Format (ANF)**.
Named the Agent Contract Protocol (ACP), it is a server-side layer that
sits on top of MCP and returns a single intent-scoped Execution Manifest
in one round trip with auth pre-injected, dependency order
pre-computed, and security boundaries declared.

We evaluate ACP across four frontier models — Claude Opus 4.7, Claude
Sonnet 4.6, GPT-5.5, and GPT-5.4 — on five representative workflows.
Using provider-reported token counts as the primary metric, ACP reduces
tool-context input tokens by **26–93%** depending on model and scenario,
with the largest gains on intent-scoped registries (S4: 50 tools, 2
relevant). A deterministic `tiktoken/cl100k_base` control confirms the
reduction is structural, not an artifact of provider tokenization.

Unlike schema-only compression libraries (TSCG) or agent-side active
discovery (MCP-Zero), ACP combines server-side intent resolution, schema
stripping, credential injection, and dependency pre-computation into one
execution manifest. The reference implementation is open source.

**Code:** `https://github.com/Clawdlinux/agent-native-format`
**License (spec):** CC BY 4.0
**License (reference runtime):** BSL 1.1, converts to Apache 2.0 on
2029-05-02

---

## 1. Introduction

Autonomous AI agents increasingly orchestrate work across many tools
exposed by Model Context Protocol (MCP) servers (Anthropic, 2024).
The protocol's design centers on tool discoverability: every connected
MCP server publishes a `tools/list` response that the agent loads in
full at the start of each session. This is appropriate for human-in-the-
loop browsing of tool catalogs; it is poorly suited to the production
case where an agent is given a specific intent and a context-window
budget.

Prior measurement work (Eckstein, 2025; Apideck, 2025; Scalekit, 2026)
documents that real MCP deployments routinely consume the majority of
the agent's context window with tool descriptors before the user's
task begins. We replicate and extend those measurements in a
controlled benchmark (Section 4) using frontier models from Anthropic
and OpenAI.

We argue that the right intervention is **not** to replace MCP, but to
add a thin layer above it that:

1. Resolves the agent's *intent* to a small, intent-scoped set of tools.
2. Compacts the verbose tool descriptors into a typed mini-language.
3. Injects credentials at a proxy boundary so secrets never enter the
   agent's context window.
4. Pre-computes the execution dependency order.
5. Returns the resulting Execution Manifest in a single HTTP round trip.

The MCP ecosystem (servers, registries, tooling) remains unchanged; an
ACP server simply consumes MCP `tools/list` payloads as one of its
sources.

## 2. Background and related work

**MCP** (Anthropic, 2024) is a JSON-RPC protocol that lets agents
discover and invoke tools published by independent servers. Its
`tools/list` response carries the full JSON-Schema for each tool's
input parameters, including descriptions, examples, and constraint
metadata.

**Tool-schema compression.** TSCG (Sakizli, 2026) is the closest
schema-level comparison. It deterministically compiles JSON tool schemas
into token-efficient structured text at the API boundary and reports
52–57% token savings across 12 models including Opus 4.7, GPT-5.2, and
Sonnet 4. TSCG is a client-side transform library; ACP is a
server-side protocol. TSCG compresses the schemas supplied to it; ACP
also decides which schemas should not be supplied at all — which is
where intent-scoped registries (S4) produce 89–93% reduction versus
TSCG's 52–57%.

**Active discovery.** MCP-Zero (Fei, Zheng, and Feng, 2025) reduces
context overhead by letting the model request tools on demand through
hierarchical semantic routing and iterative capability extension.
MCP-Zero advocates for the agent as an active discoverer. ACP takes the
opposite deployment stance: discovery autonomy is moved out of the model
loop and into a server layer. The agent sends one intent and receives
one manifest, instead of spending model turns assembling a toolchain.
MCP-Zero delivers full schemas when tools are returned; ACP strips them.
MCP-Zero has no auth injection or dependency DAG.

**MCP description quality.** Hasan et al. (2026) find that 97.1% of 856
tool descriptions across 103 MCP servers contain at least one quality
defect, and that augmenting descriptions can improve success while also
increasing execution cost. This work supports the observation that tool
descriptors are load-bearing context, but it optimizes description
quality within MCP rather than reducing the structural cost of loading
irrelevant tools.

**Code-oriented alternatives.** Cloudflare Code Mode (2025) sidesteps
descriptor cost by having agents emit code against typed SDKs rather
than enumerating tool schemas. The reduction is substantial but requires
the agent to be a code-generating model and the deployment to be on
Cloudflare.

**Agent manifests and interoperability.** AgentSpec (`agent.yaml`,
2025) is a declarative manifest for an *agent's* configuration,
dependency checks, policy, and observability. ACP is orthogonal:
AgentSpec describes the agent itself; ACP describes one execution
context for one task. An agent interoperability survey (Ehtesham et al.,
2025) also uses the acronym ACP for IBM's Agent Communication Protocol,
a RESTful agent-to-agent messaging protocol. In this paper, ACP means
**Agent Contract Protocol**, a tool-context optimization protocol rather
than an agent-to-agent messaging protocol.

**Open tool-use benchmarks.** BFCL (Patil et al., ICML 2025) provides
an open function-calling benchmark with AST-based correctness checks
and a live model leaderboard. tau-bench (Yao et al., 2024) evaluates
multi-turn tool-agent-user interaction with pass^k consistency metrics.
ACP's frontier benchmark uses the same five scenarios sent to real model
APIs; a BFCL-task mapping is the next planned track.

| Capability | TSCG | MCP-Zero | AgentSpec | **ACP** |
|---|---|---|---|---|
| Tool selection by intent | ✗ | ✓ (agent-side) | ✗ | **✓ (server-side)** |
| Schema compression | ✓ (52–57%) | ✗ | ✗ | **✓ (26–93%)** |
| Auth injection | ✗ | ✗ | ✗ | **✓** |
| Pre-computed dependency DAG | ✗ | ✗ | partial (security) | **✓** |
| Single-call execution manifest | ✗ | ✗ | ✗ | **✓** |
| Works atop existing MCP | ✗ | ✓ | ✗ | **✓** |

## 3. Protocol

### 3.1 Wire format

An ACP request is one HTTP POST:

```
POST /v1/context
Content-Type: application/json
Authorization: Bearer <agent-identity-token>

{
  "intent": "query customer data, send a slack notification",
  "agent_id": "analytics-agent-01",
  "capabilities": ["sql", "messaging"],
  "constraints": { "max_tokens": 50000, "timeout": "120s" }
}
```

The response is one Execution Manifest:

```json
{
  "manifest_id": "m-a7f3b2",
  "version": "acp/v1",
  "ttl": "300s",
  "actions": [
    { "id": "a1", "type": "http", "endpoint": "...",
      "schema": { "sql": "string", "limit": "int?" },
      "auth": "pre-injected" },
    { "id": "a2", "type": "http", "endpoint": "...",
      "schema": { "channel": "string", "text": "string" },
      "auth": "pre-injected", "depends_on": ["a1"] }
  ],
  "boundaries": {
    "egress": ["db.svc", "slack-gw.svc"],
    "max_tokens_per_action": 15000,
    "audit_level": "full"
  },
  "feedback_endpoint": "/v1/feedback"
}
```

### 3.2 Schema mini-language

ACP compacts JSON-Schema input parameters into a typed mini-language:
`string`, `int?`, `string[]`, `enum:open|closed`, `ref:<id>`. This
preserves executable meaning while removing prose descriptions,
examples, and constraint metadata. The compaction is the source of
the token reduction reported in Section 4.

### 3.3 Auth injection

The manifest's `auth` field is always `pre-injected`. The accompanying
ACP **auth proxy** mounts at `/v1/exec/{manifest_id}/{action_id}` and
forwards each action to the upstream tool with credentials added at the
proxy boundary. The agent never receives or stores credentials.

### 3.4 Dependency order

Each action carries an optional `depends_on` array of action IDs. The
agent (or an SDK) executes actions in topological order. The server
guarantees the manifest is acyclic.

### 3.5 Boundaries

Each manifest carries an `egress` allow-list of destination hosts the
proxy will permit, a `max_tokens_per_action` ceiling, an optional
`require_approval` array of actions that block on out-of-band approval,
and an `audit_level`. These are part of the contract, not advisory.

### 3.6 Relationship to MCP

ACP does not modify or replace MCP. The reference implementation
includes an MCP source adapter (`internal/sources/mcp`) that consumes
an upstream MCP server's `tools/list` payload, infers ACP capability
tags from each tool's name, compacts the JSON-Schema input parameters
to the mini-language, and registers each tool in the ACP registry.
Existing MCP servers are unmodified. Adding additional source
adapters (REST/OpenAPI, gRPC reflection, Kubernetes APIs) is a
straightforward extension.

## 4. Evaluation

### 4.1 Setup

We compare two paths for the same five workflows, sent as real API
calls to four frontier models:

- **MCP path:** full tool schemas (descriptions, examples, JSON-Schema
  constraints) provided to the model as function/tool definitions, using
  the same descriptors a real MCP server would emit.
- **ACP path:** only intent-relevant tools with stripped schemas
  (no descriptions, compact parameter types).

**Models tested:**

| Tier | Model | Provider | ID |
|---|---|---|---|
| Heavy | Claude Opus 4.7 | Anthropic | `claude-opus-4-7` |
| Heavy | GPT-5.5 | OpenAI | `gpt-5.5-2026-04-23` |
| Medium | Claude Sonnet 4.6 | Anthropic | `claude-sonnet-4-6` |
| Medium | GPT-5.4 | OpenAI | `gpt-5.4-2026-03-05` |

**Token counting methodology:** Provider-reported input/output tokens
are the primary metric — these are what determine actual cost.
A deterministic `tiktoken/cl100k_base` count over the serialized
prompt+tools payload is recorded alongside as a provider-independent
control. We run each model × scenario × path 3 times.

The five scenarios (`benchmark/scenarios/` in the repository):

| ID | Title | Tools relevant | Total tools (MCP path) |
|----|-------|----------------|-----------------------|
| S1 | Simple DB query | 1 | 1 |
| S2 | Multi-tool workflow | 3 | 3 |
| S3 | Complex DAG | 4 | 4 |
| S4 | Scale (50 registered, 2 relevant) | 2 | 50 |
| S5 | Auth-heavy cross-service | 5 | 5 |

### 4.2 Results: token reduction

Provider-reported input token reduction per model, averaged over 3 runs:

| Scenario | Opus 4.7 | Sonnet 4.6 | GPT-5.5 | GPT-5.4 |
|----------|----------|------------|---------|---------|
| S1 Simple DB | **27%** | **26%** | **55%** | **55%** |
| S2 Multi-tool | **38%** | **37%** | **65%** | **65%** |
| S3 Complex DAG | **46%** | **44%** | **71%** | **71%** |
| S4 Scale (50→2) | **89%** | **89%** | **93%** | **93%** |
| S5 Auth-heavy | **48%** | **47%** | **73%** | **73%** |

The reduction is consistent across model families and grows with the
number of irrelevant tools registered. S4 (50 tools, 2 relevant)
attains 89–93% reduction because ACP's intent resolver returns only
the two relevant tools while the MCP path loads all 50.

**Provider-reported headline numbers:**

| Model | MCP range (input tokens) | ACP range | Reduction range |
|---|---|---|---|
| Claude Opus 4.7 | 1,129 – 8,395 | 820 – 1,214 | **27–89%** |
| Claude Sonnet 4.6 | 816 – 5,979 | 603 – 916 | **26–89%** |
| GPT-5.5 | 353 – 2,630 | 159 – 254 | **55–93%** |
| GPT-5.4 | 353 – 2,631 | 159 – 254 | **55–93%** |

The tiktoken control column confirms reductions are structural:
S4 control drops from 4,169 (MCP) to 124 (ACP) tokens — a 97%
reduction independent of provider tokenization.

### 4.3 Results: tool-call correctness

Tool-call correctness measures whether the model called exactly the
expected tools in a single turn. This is a strict metric — models that
called some but not all expected tools score 0% on exact match.

All four models achieved 100% exact match on S1 (simple, 1 tool) for
both MCP and ACP paths. On multi-tool scenarios (S2–S5), exact match
was low for both paths — this is expected because a single-turn prompt
asking for 3–5 tool calls simultaneously is deliberately harder than
production usage where multi-turn execution is normal.

Critically, **ACP did not degrade correctness relative to MCP** on any
model × scenario pair. On GPT-5.4 S4, ACP improved exact match from
0% (MCP, 50 tools) to 100% (ACP, 2 tools) — the model selected both
correct tools when irrelevant tools were removed.

### 4.4 Deterministic protocol benchmark

In addition to the frontier benchmark, the repository includes a
deterministic protocol benchmark (`benchmark/harness.py`) that measures
payload overhead with no model API calls. Using `tiktoken/cl100k_base`:

| Scenario | ACP tokens / RT | MCP tokens / RT | Reduction |
|----------|------------------|------------------|-----------|
| S1 | 111 / 1 | 373 / 3 | **70.2%** |
| S2 | 295 / 1 | 837 / 5 | **64.7%** |
| S3 | 306 / 1 | 1,257 / 7 | **75.6%** |
| S4 | 241 / 1 | 9,223 / 21 | **97.4%** |
| S5 | 359 / 1 | 1,431 / 7 | **74.9%** |

Mean reduction: **76.6%**. Round trips drop from 3–21 (MCP) to 1 (ACP).
The 250 raw runs are committed at
`results/2026-05-02-week3-baseline.json`.

### 4.5 Reproducibility

All benchmark code and result artifacts are open:

- Frontier harness: `benchmark/frontier/run_frontier.py`
- Deterministic harness: `benchmark/harness.py`
- MCP payload builder: `benchmark/baseline/mcp_client.py`
- Raw frontier results: `results/frontier/*-raw.json`
- Deterministic results: `results/2026-05-02-week3-baseline.json`

```bash
git clone https://github.com/Clawdlinux/agent-native-format
cd agent-contract-protocol
python3 -m venv .venv && .venv/bin/pip install tiktoken
export $(grep -v '^#' .env | xargs)
# Frontier benchmark (requires API keys)
.venv/bin/python benchmark/frontier/run_frontier.py \
    --runs 3 --out results/frontier/
# Deterministic benchmark (no API keys needed)
go build -o bin/acp-server ./cmd/acp-server
ACP_AUTH_TOKEN=dev ./bin/acp-server --addr :18181 &
.venv/bin/python benchmark/harness.py \
    --acp-url http://127.0.0.1:18181 --auth-token dev \
    --runs 50 --out /tmp/run.json
```

### 4.6 Threats to validity

**Single-turn tool calling.** The frontier benchmark sends one prompt
and checks whether the model calls the expected tools in that single
response. Production agents typically use multi-turn execution. The
benchmark measures context overhead and tool selection, not end-to-end
task completion.

**Provider tokenizer differences.** Anthropic and OpenAI tokenize tool
schemas differently internally; this is why Anthropic S1-MCP reports
1,129 input tokens while OpenAI S1-MCP reports 353 for the same tool
set. The relative reduction within each provider is the meaningful
comparison. The `tiktoken` control normalizes across providers.

**MCP descriptor verbosity.** Our MCP baseline matches the verbosity
of widely-deployed MCP servers (GitHub, Slack, Sentry); a hypothetical
minimal MCP server with empty descriptions would narrow the gap.

**Intent resolver scope.** The current ACP resolver uses keyword
matching. An embedding-based resolver would improve recall on ambiguous
intents but would not change the protocol-level token reduction.

## 5. Implementation

The reference implementation is approximately 4,500 lines of Go
(server, proxy, registry, builder, MCP source adapter) and 1,200 lines
of Python (adapter packages, benchmark harnesses). Test coverage is 95%
on the Go core and 96.8% on the Python tooling, including fuzz
coverage for the schema compactor and dependency resolver, and a live
integration test that exercises the MCP-source-to-ACP-manifest path
end to end.

The Go SDK is at `pkg/acp`; Python adapters at
`adapters/python/{acp_common, acp_openai, acp_langgraph, acp_crewai}`.
The auth-injection proxy is at `internal/proxy`; the MCP source
adapter at `internal/sources/mcp`.

## 6. Conclusion

We have introduced ACP, an intent-resolved execution-contract layer
that sits on top of MCP and other tool sources. Across five scenarios
and four frontier models (Opus 4.7, Sonnet 4.6, GPT-5.5, GPT-5.4),
ACP reduces provider-reported tool-context input tokens by 26–93%
(mean ~60%) and in the deterministic protocol benchmark by 64.7–97.4%
(mean 76.6%). Round trips before the first useful action drop from 3–21
to 1 in all cases. Tool-call correctness is preserved or improved.

The white space ACP uniquely owns is the combination of server-side
intent resolution, schema stripping, credential injection, and
dependency pre-computation in a single-call protocol that leaves the
MCP ecosystem unchanged. No prior work combines all five.

The next development priorities are:

1. A BFCL-task mapping to measure ACP's impact on AST-evaluated
   function-calling correctness across the full leaderboard.
2. An embedding-based intent resolver for production deployments where
   intent vocabulary is more varied than benchmark scenarios.
3. Additional source adapters: REST/OpenAPI, gRPC reflection, the
   Kubernetes-native `agentic-operator` for regulated clusters.
4. A feedback-driven optimizer that improves manifest compactness and
   accuracy with deployment data.

We welcome contributions and would particularly value field reports
from teams running multi-tool agent systems at scale.

## References

1. Anthropic. *Model Context Protocol Specification*. 2024–2026.
   `https://modelcontextprotocol.io/`
2. Eckstein, B. *Replacing MCP servers with shell scripts*. 2025.
3. Apideck. *Measuring agent context window utilization with MCP*.
   2025.
4. Scalekit. *MCP vs CLI token cost: 75 head-to-head comparisons*.
   2026.
5. Sakizli, F. *TSCG: Deterministic Tool-Schema Compilation for
   Agentic LLM Deployments*. arXiv:2605.04107. 2026.
6. Fei, X., Zheng, X., and Feng, H. *MCP-Zero: Active Tool Discovery
   for Autonomous LLM Agents*. arXiv:2506.01056. 2025.
7. Hasan, M. M., et al. *Model Context Protocol (MCP) Tool
   Descriptions Are Smelly!*. arXiv:2602.14878. 2026.
8. Ehtesham, A., et al. *A survey of agent interoperability
   protocols*. arXiv:2505.02279. 2025.
9. Patil, S. G., et al. *The Berkeley Function Calling Leaderboard
   (BFCL)*. ICML 2025.
10. Yao, S., et al. *tau-bench: A Benchmark for Tool-Agent-User
    Interaction in Real-World Domains*. arXiv:2406.12045. 2024.
11. Cloudflare. *Code Mode for AI agents*. 2025.
12. Google. *A2A Agent Cards*. 2025.
13. *AgentSpec: a declarative manifest for AI agents*. 2025.
14. Oracle. *Open Agent Spec*. 2025.

(See `paper/references.bib` for full BibTeX entries.)

---

*Draft updated: 2026-05-13. Frontier benchmark: 120 API calls across
Opus 4.7, Sonnet 4.6, GPT-5.5, GPT-5.4. Raw results committed under
`results/frontier/`. Manuscript source:
`https://github.com/Clawdlinux/agent-native-format/blob/main/paper/acp.md`*