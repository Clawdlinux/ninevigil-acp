# Agent Native Format (ANF) Specification

**Version:** 0.1.0-draft  
**Status:** Draft  
**License:** CC BY 4.0  
**Authors:** Shreyansh Sancheti (Clawdlinux)
**Date:** 2026-05-03

---

## Abstract

Agent Native Format (ANF) is a line-oriented, token-minimal representation
language designed for AI agent consumption. Where JSON, YAML, and HTML are
built for human readability, ANF is built for LLM token efficiency —
encoding maximum decision-relevant information in minimum context window
space.

ANF is not a general-purpose data format. It is a **view format**: a
translated, compressed representation of system state that an AI agent
needs to make decisions and take actions. The source systems (Kubernetes
APIs, SaaS platforms, databases) remain unchanged. ANF is the translation
layer between them and the agents that consume them.

## 1. Design Principles

1. **Token density over human readability.** Every token must carry
   decision-relevant information. No boilerplate, no decorative syntax.
2. **Self-describing without schemas.** An LLM can parse and reason about
   ANF without a separate schema definition. Field names are chosen to be
   unambiguous single tokens.
3. **Decision-oriented structure.** Health status, alerts, and available
   actions surface first — not buried in nested data structures.
4. **Line-oriented.** One logical unit per line. Agents can scan, skip,
   and focus on relevant lines without parsing nested structures.
5. **Domain-portable.** The same syntax works for Kubernetes clusters,
   SaaS dashboards, database state, CI/CD pipelines — any operational
   domain.

## 2. Token Efficiency Rationale

LLM tokenizers (BPE-based: cl100k_base, o200k_base) assign single tokens
to common English words, short punctuation, and whitespace. ANF exploits
this:

- **No quotes.** JSON `"key": "value"` costs 5 tokens. ANF `key:value`
  costs 2-3 tokens.
- **No braces or brackets.** Hierarchy via indentation (spaces are cheap:
  4 spaces = 1 token).
- **No commas.** Line breaks are delimiters (newline = 1 token).
- **Semantic prefixes.** `@`, `!`, `?`, `>` are single tokens that encode
  meaning (metadata, alert, action, output).
- **Compact values.** `3/3` not `{"desired": 3, "ready": 3}`. `42%` not
  `{"percentage": 42}`.

Measured: a Kubernetes namespace view that costs **~12,000 tokens** as raw
API JSON and **~2,400 tokens** as filtered JSON costs **~350 tokens** in
ANF.

## 3. Syntax

### 3.1 Document Structure

An ANF document has four sections in fixed order. All are optional except
at least one entity.

```
[headers]       @-prefixed metadata lines
[entities]      Indented hierarchical state
[alerts]        !-prefixed warnings/issues
[actions]       ?-prefixed available operations
```

### 3.2 Headers

Headers provide scope and context. Prefix: `@`

```
@source kubernetes/prod-east
@scope namespace:payments
@time 2026-05-03T10:30:00Z
@ttl 60s
@translator clawdlinux/k8s-translator:0.1.0
```

| Header | Required | Description |
|--------|----------|-------------|
| `@source` | yes | Origin system and identifier |
| `@scope` | no | Narrowing filter applied |
| `@time` | yes | Snapshot timestamp (ISO 8601) |
| `@ttl` | no | Seconds until this view is stale |
| `@translator` | no | Translator that produced this view |

### 3.3 Entities

Entities are the core data. They follow this grammar:

```
<type> <name> [<status>] [<inline-props>]
  <prop> <value>
  <prop> <value>
  <child-type> <child-name> [<status>] [<inline-props>]
```

**Types** are lowercase singular nouns: `deployment`, `pod`, `service`,
`node`, `job`, `table`, `channel`, `pipeline`.

**Status markers** are bracketed keywords:

| Marker | Meaning |
|--------|---------|
| `[healthy]` | All checks passing |
| `[degraded]` | Partially functional |
| `[failing]` | Critical failure |
| `[pending]` | Waiting / not yet ready |
| `[terminated]` | Stopped / completed |
| `[unknown]` | Status cannot be determined |

**Inline properties** appear on the same line as the entity, space-separated:

```
deployment payment-api [healthy] replicas:3/3 age:14d
```

**Indented properties** appear on subsequent lines, 2-space indent:

```
deployment payment-api [healthy] replicas:3/3 age:14d
  image registry.io/payment-api:v2.4.1
  strategy rolling maxSurge:1
  cpu 42% mem 61%
```

**Child entities** are indented under their parent:

```
deployment payment-api [healthy] replicas:3/3
  pod payment-api-7f8d [running] cpu:42% mem:61% restarts:0
  pod payment-api-9a2b [running] cpu:38% mem:55% restarts:0
```

### 3.4 Property Values

Values use compact notation:

| Pattern | Meaning | Example |
|---------|---------|---------|
| `N/M` | Ratio (ready/desired) | `replicas:3/3` |
| `N%` | Percentage | `cpu:42%` |
| `Ns`, `Nm`, `Nh`, `Nd` | Duration | `age:14d`, `timeout:30s` |
| `N` | Integer | `restarts:3` |
| `N.N` | Float | `cost:0.042` |
| `key:value` | Labeled value | `image:v2.4.1` |
| Bare word | String value | `strategy rolling` |

### 3.5 Alerts

Alerts surface issues that need attention. Prefix: `!`

```
!critical pod payment-worker-9a2b OOMKilled restarts:5/1h
!warning deployment payment-worker mem:89% threshold:80%
!info deployment payment-api image-outdated latest:v2.5.0
```

Severity levels: `!critical`, `!warning`, `!info`.

Alerts always reference an entity by `<type> <name>`.

### 3.6 Actions

Actions declare what operations the agent can perform. Prefix: `?`

```
?scale deployment payment-api range:1-10 current:3
?scale deployment payment-worker range:1-5 current:2
?rollout deployment payment-api to:v2.5.0 strategy:rolling
?restart deployment payment-worker
?logs pod payment-worker-9a2b lines:100 since:1h
?exec pod payment-worker-9a2b shell:/bin/sh
?describe any
```

Grammar: `?<verb> <target-type> <target-name> [<params>]`

Actions are the critical differentiator from data formats. ANF doesn't
just describe state — it tells the agent what it **can do** in response
to that state. The translation layer computes available actions from the
agent's permissions, the current state, and operational policies.

### 3.7 Comments

Lines starting with `#` are comments. They are stripped before token
counting and should not be included in production output.

```
# This is for debugging only
```

### 3.8 Multi-View Documents

Multiple views can be concatenated with `---` separators:

```
@source kubernetes/prod-east
@scope namespace:payments
...
---
@source slack/workspace
@scope channel:#payments-alerts
...
```

## 4. Full Example

### 4.1 Kubernetes Namespace View

```
@source kubernetes/prod-east
@scope namespace:payments
@time 2026-05-03T10:30:00Z
@ttl 60s

deployment payment-api [healthy] replicas:3/3 age:14d
  image registry.io/payments/api:v2.4.1
  strategy rolling maxSurge:1 maxUnavail:0
  cpu 42% mem 61% requests:1.2k/s errors:0.02%
  pod payment-api-7f8d [running] node:worker-01 cpu:45% mem:63% restarts:0
  pod payment-api-9a2b [running] node:worker-02 cpu:38% mem:55% restarts:0
  pod payment-api-1c4e [running] node:worker-01 cpu:44% mem:64% restarts:0

deployment payment-worker [degraded] replicas:2/2 age:8d
  image registry.io/payments/worker:v1.8.0
  cpu 87% mem 78%
  pod payment-worker-a3f1 [running] node:worker-03 cpu:87% mem:78% restarts:3/24h
  pod payment-worker-b7e2 [running] node:worker-03 cpu:82% mem:74% restarts:1/24h

service payment-api ClusterIP 8080>8080 endpoints:3/3
service payment-grpc ClusterIP 9090>9090 endpoints:2/2

job daily-reconciliation [completed] last:2026-05-03T06:00:00Z duration:4m success:true
cronjob hourly-sync schedule:0_*_*_*_* last-run:2026-05-03T10:00:00Z next:2026-05-03T11:00:00Z

!warning deployment payment-worker mem:89% threshold:80%
!warning pod payment-worker-a3f1 restarts:3/24h threshold:2
!info deployment payment-api image-outdated latest:v2.5.0 current:v2.4.1

?scale deployment payment-api range:1-10 current:3
?scale deployment payment-worker range:1-5 current:2
?rollout deployment payment-api to:v2.5.0 strategy:rolling
?restart deployment payment-worker
?logs pod payment-worker-a3f1 since:1h
?exec pod payment-worker-a3f1 shell:/bin/sh
```

### 4.2 Token Comparison

The same Kubernetes namespace state represented three ways:

| Format | Tokens (cl100k_base) | Reduction vs JSON |
|--------|---------------------|-------------------|
| Raw Kubernetes API JSON | ~12,000 | — |
| Filtered JSON (relevant fields only) | ~2,400 | 80% |
| ANF (above) | ~350 | **97%** |

The 80% reduction from JSON filtering is what existing MCP optimization
tools achieve. The additional 85% reduction from filtered JSON to ANF
is what the agent-native format provides — encoding the same information
in a syntax designed for how LLMs tokenize.

## 5. Translation Architecture

ANF is produced by **translators** — domain-specific components that
convert raw system state into ANF views.

```
┌──────────┐     ┌──────────────┐     ┌──────────┐
│ K8s API  │────>│ K8s          │────>│          │
└──────────┘     │ Translator   │     │          │
                 └──────────────┘     │          │
┌──────────┐     ┌──────────────┐     │   ANF    │
│ Slack API│────>│ Slack        │────>│   View   │──> Agent
└──────────┘     │ Translator   │     │          │
                 └──────────────┘     │          │
┌──────────┐     ┌──────────────┐     │          │
│ PG/MySQL │────>│ Database     │────>│          │
└──────────┘     │ Translator   │     └──────────┘
                 └──────────────┘
```

A translator MUST:
1. Fetch current state from the source system
2. Filter to decision-relevant information
3. Compute health status from raw metrics
4. Determine available actions from permissions + state
5. Emit valid ANF

A translator SHOULD:
- Cache source responses within the TTL
- Log which fields were accessed by agents (for optimization)
- Strip fields that agents consistently ignore

## 6. Parsing

ANF is intentionally simple to parse. The grammar is:

```
document    = (header | entity | alert | action | separator | comment)*
header      = "@" key SP value NL
entity      = INDENT type SP name (SP status)? (SP prop)* NL (child)*
child       = INDENT INDENT (entity | prop-line)
prop-line   = INDENT INDENT key (SP value)* NL
alert       = "!" severity SP type SP name SP message (SP prop)* NL
action      = "?" verb SP type SP name (SP param)* NL
separator   = "---" NL
comment     = "#" text NL
status      = "[" word "]"
prop        = key ":" value
```

Reference parsers are provided in Go and Python in the agent-contract-protocol
repository.

## 7. Extending ANF for New Domains

To add a new domain (e.g., CI/CD pipelines):

1. Define entity types: `pipeline`, `stage`, `step`, `artifact`
2. Define status markers (same vocabulary: healthy/degraded/failing/...)
3. Define action verbs: `?trigger`, `?cancel`, `?retry`, `?approve`
4. Write a translator that maps the source API → ANF

The core syntax does not change. Domain vocabulary is additive.

### 7.1 SaaS Example

```
@source salesforce/prod
@scope opportunity pipeline:enterprise
@time 2026-05-03T10:30:00Z

opportunity Acme-Corp-Renewal [at-risk] value:$450k stage:negotiation age:62d
  owner Sarah Chen
  next-step "Send revised proposal" due:2026-05-05
  competitor Datadog mentioned:3x

opportunity BigBank-Expansion [healthy] value:$1.2M stage:evaluation age:28d
  owner James Wu
  next-step "Technical deep-dive scheduled" due:2026-05-07

!warning opportunity Acme-Corp-Renewal stale:14d no-activity-since:2026-04-19
!info pipeline close-rate:32% target:40% gap:-8pp

?update opportunity Acme-Corp-Renewal stage:proposal
?create task owner:Sarah-Chen "Follow up on revised pricing"
?forecast pipeline method:weighted
```

## 8. Security

- ANF views MUST NOT contain credentials, tokens, or secrets.
- Translators MUST filter sensitive fields (env vars with secret values,
  annotations with tokens) before emitting ANF.
- The `@scope` header declares what the view covers — agents should not
  assume they have visibility beyond the stated scope.
- Action availability (`?` lines) reflects the agent's actual permissions.
  If the agent cannot scale a deployment, that action does not appear.

## 9. Versioning

The format version is implicit in the translator version (`@translator`
header). The core syntax (prefixes, indentation, status markers) is
stable across versions. Domain vocabulary may expand but never removes
existing entity types or status markers.

---

*This specification is licensed under [CC BY 4.0](https://creativecommons.org/licenses/by/4.0/).
You are free to share and adapt this material for any purpose, including
commercial, with attribution.*
