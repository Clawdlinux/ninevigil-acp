# Decision Log

## 2026-05-02 — Start ACP as a private Clawdlinux repo

**Decision:** Create `Clawdlinux/agent-native-format` as a private repository and
build the PoC independently from `agentic-operator-core`.

**Why:** ACP is a new protocol and benchmark artifact. Keeping it separate
protects the invention while allowing `agentic-operator-core` to become the
future enterprise deployment mechanism.

## 2026-05-02 — End-of-phase documentation cadence

**Decision:** Use end-of-phase verifier/doc passes instead of a persistent
background agent inside the chat session.

**Why:** Copilot subagents are one-shot. A per-phase documentation pass is
reliable, auditable, and can mirror repo state into Obsidian without pretending
there is a persistent background process.

## 2026-05-02 — Split license strategy

**Decision:** Spec under CC BY 4.0, SDKs/adapters under Apache 2.0, server
runtime under BSL 1.1 with 3-year Apache conversion.

**Why:** This maximizes ecosystem adoption for the protocol while protecting
the optimized runtime until Clawdlinux has a data and deployment moat.
