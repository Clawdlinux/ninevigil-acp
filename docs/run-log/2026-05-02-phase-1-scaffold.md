# 2026-05-02 — Phase 1 Scaffold Run

## Objective

Start ACP as a new private Clawdlinux invention repo with a durable
protocol spec, scaffold, benchmark plan, and Obsidian mirror.

## Inputs

- Source document: `/Users/sunny/Documents/Claude/Projects/Kubernetes Agent Swarms for Enterprise/ACP_PoC_Specification_CONFIDENTIAL.docx`
- GitHub org: `clawdlinux`
- Repo: `Clawdlinux/agent-native-format`
- Local path: `/Users/sunny/clawdlinux/agent-contract-protocol`
- Obsidian vault folder: `ACP-PoC/` inside the Clawdlinux vault

## Work completed

- Created private GitHub repo `Clawdlinux/agent-native-format`.
- Cloned repo to `/Users/sunny/clawdlinux/agent-contract-protocol`.
- Created source-spec scaffold:
  - `cmd/acp-server/`
  - `cmd/benchmark/`
  - `internal/{manifest,registry,resolver,proxy}/`
  - `pkg/{acp,manifest}/`
  - `adapters/python/{acp_langgraph,acp_crewai,acp_openai}/`
  - `benchmark/{scenarios,baseline}/`
  - `results/`
  - `docs/`
  - `deploy/k8s/`
- Wrote `SPEC.md` v0.1 from the source document.
- Wrote README, licensing notes, architecture notes, protocol notes, benchmark
  methodology, pitch-deck placeholder, references, and decision log.
- Added S1-S5 benchmark scenario YAML definitions.
- Added minimal public Go package stubs so `go test ./...` is meaningful.
- Added CI placeholder and `scripts/check_docs.py`.
- Seeded Obsidian notes:
  - `ACP-PoC/Index.md`
  - `ACP-PoC/Phase Log.md`
  - `ACP-PoC/Pitch Notes.md`

## Verifier pass

Verifier identified:

1. Missing scaffold files from source spec: `benchmark/baseline/mcp_client.py`,
   `benchmark/harness.py`, `benchmark/report.py`.
2. Stale README/phase-log reference to `SPEC.md §3.3`.
3. Ambiguous Obsidian folder wording.

Fixes applied before commit.

## Validation

Commands run:

```bash
python3 scripts/check_docs.py
go test ./...
rg -n "<stale-reference-and-credential-patterns>" . --hidden -g '!go.sum'
```

Results:

- Docs check: pass.
- Go test: pass for `pkg/acp` and `pkg/manifest`.
- Stale reference / secret scan: no matches.

## Next phase

Week 1 runtime implementation:

1. Define manifest request/response Go types.
2. Implement in-memory tool registry.
3. Implement keyword capability resolver.
4. Implement manifest builder with compact schemas and `depends_on`.
5. Add `cmd/acp-server` with `POST /v1/context`, `POST /v1/feedback`, and
   `GET /healthz`.
6. Replace placeholder Compose service with real server build.
