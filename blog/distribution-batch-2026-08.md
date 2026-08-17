# Clawdlinux distribution batch

Anonymous drafts for developer and platform-engineering channels.

Use the visual listed with each post. Keep the author field empty.
Do not claim customer adoption, air-gap certification, or complete end-to-end attestation.

## Post 1: The boundary question

AI agents are moving from demos into production systems.

The hard question is not whether an agent can call a tool.

It is what happens when the tool call is wrong.

Who is the agent acting as?
What can it reach?
Who approves the action?
What record can an auditor verify later?

Kubernetes gives teams the place to answer those questions.
It does not answer them by itself.

Clawdlinux is an open Kubernetes governance layer for AI agent workloads.
It adds runtime-neutral workload controls, egress policy generation, and offline audit verification primitives.

The current repository is honest about its limits.
Some controls are generated but depend on cluster enforcement.
The full signed artifact path is still a release gate.

That is a better starting point than pretending the problem is solved.

Repo: https://github.com/Clawdlinux/agentic-operator-core

Visual: https://raw.githubusercontent.com/Clawdlinux/agentic-operator-core/main/assets/landing-hero.png

## Post 2: Agent egress is a practical control

Prompt instructions are not a network boundary.

An agent can be told not to call an endpoint.
A compromised tool can still try.

A declared egress policy gives the cluster a second control point.

The Clawdlinux operator can generate default-deny and allow-list policy objects for managed workloads.
Cilium FQDN policy templates are available when the cluster supports them.

The policy is not magic.
The CNI must enforce it.
The selectors must be correct.
The workload must declare the destinations it needs.

That is the practical model for Kubernetes AI agent egress policy:
make the allowed boundary explicit, then verify the cluster enforces it.

Docs: https://clawdlinux.org/products/operator

Visual: Mermaid diagram showing AgentWorkload -> admission -> policy -> sandboxed pod -> allowed endpoint.

## Post 3: Offline audit evidence

A log line is not automatically evidence.

If an auditor can edit the record without detection, the record is weak.

The Clawdlinux operator repository includes HMAC hash-chain and JSONL verification primitives.
The `audit-verify` path can verify exported records without a live control plane.

That matters for air-gapped AI agent deployment.
The auditor may not have access to the production cluster.
The verifier still needs a stable artifact and a clear failure result.

The current code does not claim that every run already emits a complete signed artifact.
That connection remains a release gate.

The useful design question is simple:
what exact file does the auditor receive, and what command proves it was not changed?

Repo: https://github.com/Clawdlinux/agentic-operator-core

Visual: before/after terminal capture of a clean verification and a deliberate tamper failure.

## Post 4: Runtime-neutral means less lock-in

A governance layer should not require one agent runtime.

Teams already have choices:
- a custom pod
- a workflow engine
- a runtime with its own agent resource

The Clawdlinux operator uses runtime adapters around the workload boundary.
The governance controls stay at the pod and network layers.

That keeps the hard control in one place:
isolation, egress, cost paths, and audit primitives apply to labeled workloads.

The adapter chooses how a workload starts.
It should not reimplement the security model.

This is the boring architecture choice.
Boring is useful when a platform team has to maintain it.

Docs: https://github.com/Clawdlinux/agentic-operator-core/tree/main/pkg/runtime

Visual: one diagram with three runtime inputs converging on the same policy and evidence boundary.

## Post 5: A Helm chart where operators search

Platform engineers do not need another landing page first.
They need a chart they can inspect and pull.

The Clawdlinux operator chart is published as a public OCI artifact.

```bash
helm pull oci://ghcr.io/clawdlinux/agentic-operator-core/agentic-operator \
  --version 0.1.0
```

The chart metadata describes the real scope:
Kubernetes agent governance, gVisor isolation, egress policy, cost controls, and audit verification.

Artifact Hub listing:
https://artifacthub.io/packages/helm/clawdlinux-operator/agentic-operator

The first install still needs a real cluster review.
Run it with the CNI, RuntimeClass, storage, and image assumptions your environment supports.

Repo: https://github.com/Clawdlinux/agentic-operator-core

Visual: Artifact Hub package screenshot beside the `helm pull` command.

## Post 6: What this does not claim

Clawdlinux is not a claim that Kubernetes makes agents safe by default.

It is not a claim that generated NetworkPolicy objects are enforced by every CNI.
It is not a claim that every run already produces a complete signed attestation.
It is not a claim of FedRAMP, HIPAA, or air-gap certification.

It is a set of Kubernetes governance and evidence primitives:
- runtime-neutral workload adapters
- gVisor RuntimeClass mutation for labeled pods
- generated network policy objects
- cost and model-routing paths
- offline JSONL and hash-chain verification tools

The practical test is small.
Pick one workload.
Declare its allowed destinations.
Run the cluster checks.
Export the record.
Verify it offline.

If that workflow does not work in your environment, the right result is a bug report or a missing control.
Not a marketing claim.

Repo: https://github.com/Clawdlinux/agentic-operator-core
Visual: one screenshot of the actual verification command and its output.

## Distribution checklist

- [ ] Add the public OCI chart to Artifact Hub. Registration is complete; wait for processing.
- [ ] Verify the Artifact Hub package page after processing.
- [ ] Request Google Search Console reindex for `https://clawdlinux.org/` and `/products/operator`.
- [ ] Publish one post at a time. Do not schedule all six without feedback.
- [ ] Use the raw hero image for Post 1 only.
- [ ] Use real terminal captures for Posts 3 and 5.
- [ ] Do not publish an anonymous post under a fake person or fake customer.
- [ ] Keep the Clawdlinux brand visible. Keep the author identity optional.
- [ ] Track installs, replies, and qualified conversations. Do not use impressions as validation.
