"""MCP-equivalent payload builder for benchmarking.

This module synthesizes the JSON payloads an MCP-aware agent would have to
load into its context window before performing useful work, given the same
set of tools that the ACP server exposes.

Why not use the official MCP SDK?

The benchmark question is *byte/token cost in the agent's context window*,
not transport correctness. For that, we need a faithful representation of
the *shape* and *verbosity* of MCP `tools/list` responses, which the
specification mandates and the popular SDKs emit. We rebuild that shape
from a tool descriptor table we control, so the comparison is reproducible
without depending on any specific MCP server vendor.

The shape we emit follows the MCP 2024-11 schema:

    {
      "tools": [
        {
          "name": "...",
          "description": "...",
          "inputSchema": {
            "type": "object",
            "properties": {...},
            "required": [...],
            "additionalProperties": false,
            "$schema": "https://json-schema.org/draft/2020-12/schema"
          }
        },
        ...
      ]
    }

For multi-server scenarios, the agent receives one such payload per server.

Reference:
    https://modelcontextprotocol.io/specification/2024-11-05/server/tools
"""

from __future__ import annotations

import json
from dataclasses import dataclass, field
from typing import Any


@dataclass(frozen=True)
class ToolDescriptor:
    """A single MCP tool descriptor."""

    name: str
    description: str
    properties: dict[str, dict[str, Any]] = field(default_factory=dict)
    required: list[str] = field(default_factory=list)
    examples: list[dict[str, Any]] = field(default_factory=list)

    def to_mcp(self) -> dict[str, Any]:
        """Render in the canonical MCP `tools/list` element shape."""
        schema: dict[str, Any] = {
            "$schema": "https://json-schema.org/draft/2020-12/schema",
            "type": "object",
            "additionalProperties": False,
            "properties": self.properties,
            "required": self.required,
        }
        if self.examples:
            schema["examples"] = self.examples
        return {
            "name": self.name,
            "description": self.description,
            "inputSchema": schema,
        }


# Tool catalog matched to the ACP server's seeded tools (see
# internal/registry/seed.go). Descriptions and schemas are written to match
# the verbosity of real-world MCP servers (GitHub, Slack, Sentry tool
# descriptors range from 1.5 KB to 12 KB each per the survey cited in
# docs/references.md).
CATALOG: dict[str, ToolDescriptor] = {
    "db.query": ToolDescriptor(
        name="db.query",
        description=(
            "Execute a read-only SQL query against the customer analytics "
            "warehouse. Returns rows as a list of objects keyed by column "
            "name. Supports SELECT statements only; mutations and DDL are "
            "rejected at the proxy. The optional `limit` parameter caps "
            "the number of rows returned (default 1000, maximum 50000). "
            "Use this when you need to look up customer records, "
            "aggregate metrics, or run ad-hoc analytical queries. Do not "
            "use for schema introspection (use db.describe instead) or "
            "for write operations (use db.exec instead)."
        ),
        properties={
            "sql": {
                "type": "string",
                "description": (
                    "A read-only SQL statement. Must begin with SELECT, "
                    "WITH, or EXPLAIN. Maximum length is 16384 characters. "
                    "Identifiers must be schema-qualified."
                ),
                "minLength": 1,
                "maxLength": 16384,
            },
            "limit": {
                "type": "integer",
                "description": (
                    "Maximum number of rows to return. Defaults to 1000 "
                    "if omitted. Capped at 50000 by the server."
                ),
                "minimum": 1,
                "maximum": 50000,
            },
        },
        required=["sql"],
        examples=[
            {"sql": "SELECT id, name FROM customer LIMIT 10", "limit": 10},
            {"sql": "SELECT COUNT(*) AS total FROM orders WHERE created_at > NOW() - INTERVAL '7 days'"},
        ],
    ),
    "template.render": ToolDescriptor(
        name="template.render",
        description=(
            "Render a Jinja2 template registered with the templating "
            "service and return the produced text. The template_id must "
            "refer to a template that has been published to the template "
            "registry. The data parameter provides the variables passed "
            "to the template at render time. Returns a string containing "
            "the rendered output. Use this for generating reports, "
            "emails, and other structured documents from data."
        ),
        properties={
            "template_id": {
                "type": "string",
                "description": (
                    "ID of the template to render. Must match a published "
                    "template in the registry."
                ),
                "minLength": 1,
                "maxLength": 256,
            },
            "data": {
                "type": "object",
                "description": (
                    "Variables passed to the template as the rendering "
                    "context. Must be JSON-serializable."
                ),
                "additionalProperties": True,
            },
        },
        required=["template_id", "data"],
        examples=[
            {"template_id": "weekly-summary-v3", "data": {"week": 18, "topline_revenue": 124000}},
        ],
    ),
    "email.send": ToolDescriptor(
        name="email.send",
        description=(
            "Send an email via the corporate email gateway. Subject is "
            "required and must be 1-200 characters. The body field "
            "supports plain text or HTML; the gateway sniffs the content "
            "type. Recipients in `to` are validated against the corporate "
            "directory; unknown addresses are rejected. Optional "
            "attachment_ref points to a file already uploaded to the "
            "object store; the gateway downloads and attaches it before "
            "sending. This action requires human approval before "
            "execution; the proxy will block until approval is recorded."
        ),
        properties={
            "to": {
                "type": "array",
                "description": "Recipient email addresses.",
                "items": {"type": "string", "format": "email"},
                "minItems": 1,
                "maxItems": 50,
            },
            "subject": {
                "type": "string",
                "description": "Email subject line.",
                "minLength": 1,
                "maxLength": 200,
            },
            "body": {
                "type": "string",
                "description": "Email body. Plain text or HTML.",
                "minLength": 1,
                "maxLength": 1048576,
            },
            "attachment_ref": {
                "type": "string",
                "description": (
                    "Reference to a previously uploaded file in the "
                    "object store. Format: `obj://<bucket>/<key>`."
                ),
            },
        },
        required=["to", "subject", "body"],
        examples=[
            {
                "to": ["team-finance@example.com"],
                "subject": "Weekly revenue summary",
                "body": "<p>Attached is the weekly summary.</p>",
                "attachment_ref": "obj://reports/weekly-2026-w18.pdf",
            }
        ],
    ),
    "slack.send_message": ToolDescriptor(
        name="slack.send_message",
        description=(
            "Post a message to a Slack channel. The channel field accepts "
            "either a channel ID (Cxxxxx) or a channel name with leading "
            "hash (e.g. #general). Text supports Slack's mrkdwn formatting "
            "syntax. Returns the timestamp of the posted message which can "
            "be used for threading."
        ),
        properties={
            "channel": {
                "type": "string",
                "description": "Slack channel ID or #channel-name.",
                "minLength": 1,
                "maxLength": 80,
            },
            "text": {
                "type": "string",
                "description": "Message text. Supports mrkdwn.",
                "minLength": 1,
                "maxLength": 40000,
            },
        },
        required=["channel", "text"],
        examples=[{"channel": "#alerts", "text": ":fire: pipeline failed"}],
    ),
    "audit.log_event": ToolDescriptor(
        name="audit.log_event",
        description=(
            "Append an event to the immutable audit log. The actor field "
            "should identify the agent or user performing the action. "
            "The action field is a short verb describing what happened. "
            "The payload is an arbitrary JSON object captured for "
            "compliance review. Events are timestamped server-side and "
            "are tamper-evident."
        ),
        properties={
            "actor": {
                "type": "string",
                "description": "ID of the agent or user.",
                "minLength": 1,
                "maxLength": 256,
            },
            "action": {
                "type": "string",
                "description": "Verb describing the action.",
                "minLength": 1,
                "maxLength": 128,
            },
            "payload": {
                "type": "object",
                "description": "Arbitrary JSON payload for review.",
                "additionalProperties": True,
            },
        },
        required=["actor", "action", "payload"],
        examples=[
            {
                "actor": "analytics-agent-01",
                "action": "exported_customer_report",
                "payload": {"rows": 1234, "destination": "team-finance@example.com"},
            }
        ],
    ),
}


# Per the MCP spec, an `initialize` handshake precedes `tools/list`. Real
# clients also load resources, prompts, and capabilities. We model only the
# pieces that *must* sit in the agent's context window for tool calling.
INITIALIZE_RESPONSE: dict[str, Any] = {
    "protocolVersion": "2024-11-05",
    "capabilities": {
        "tools": {"listChanged": True},
        "resources": {"subscribe": True, "listChanged": True},
        "prompts": {"listChanged": True},
        "logging": {},
    },
    "serverInfo": {"name": "clawdlinux-mcp-baseline", "version": "0.1.0"},
}


def tools_list_payload(tool_ids: list[str]) -> dict[str, Any]:
    """Return the body of an MCP ``tools/list`` response for the given IDs."""
    missing = [t for t in tool_ids if t not in CATALOG]
    if missing:
        raise KeyError(f"unknown tools: {missing}")
    return {"tools": [CATALOG[t].to_mcp() for t in tool_ids]}


def _noise_tool_descriptor(index: int) -> dict[str, Any]:
    """Build a generic noise tool descriptor for the scale scenario.

    Real MCP servers commonly register many tools per service (GitHub's MCP
    server alone registers 40+ tools). We model that by cloning a realistic
    descriptor shape so noise tokens reflect production verbosity rather than
    a hand-shrunk strawman.
    """
    return {
        "name": f"misc.tool_{index:03d}",
        "description": (
            f"Auxiliary tool #{index} registered by the MCP server. Performs "
            "an operation against the upstream service. Accepts a structured "
            "input object and returns a structured response. Refer to the "
            "service documentation for usage guidance and rate limits."
        ),
        "inputSchema": {
            "$schema": "https://json-schema.org/draft/2020-12/schema",
            "type": "object",
            "additionalProperties": False,
            "properties": {
                "operation": {
                    "type": "string",
                    "description": "Operation identifier within the tool's namespace.",
                    "minLength": 1,
                    "maxLength": 128,
                },
                "params": {
                    "type": "object",
                    "description": "Free-form parameter object passed to the operation.",
                    "additionalProperties": True,
                },
                "options": {
                    "type": "object",
                    "description": "Operational options (timeouts, retries, idempotency keys).",
                    "additionalProperties": True,
                },
            },
            "required": ["operation"],
        },
    }


def mcp_context_payload(
    tool_ids: list[str],
    num_servers: int = 1,
    noise_tools: int = 0,
) -> str:
    """Return the JSON the agent must keep in context to use the tools.

    ``num_servers`` lets us model the multi-server case (each server emits
    its own initialize + tools/list pair).

    ``noise_tools`` adds N additional generic tool descriptors spread across
    the same servers - models the "intent scoping at scale" case where many
    irrelevant tools are registered alongside the few the agent needs.
    """
    if num_servers < 1:
        raise ValueError("num_servers must be >= 1")
    if noise_tools < 0:
        raise ValueError("noise_tools must be >= 0")

    parts: list[Any] = []
    per_server = max(1, len(tool_ids) // num_servers)
    noise_per_server = noise_tools // num_servers
    noise_remainder = noise_tools - noise_per_server * num_servers
    next_noise_index = 0
    for i in range(num_servers):
        if i < num_servers - 1:
            slice_ = tool_ids[i * per_server : (i + 1) * per_server]
        else:
            slice_ = tool_ids[i * per_server :]
        relevant = (
            tools_list_payload(slice_)["tools"] if slice_ else []
        )
        n_noise = noise_per_server + (1 if i < noise_remainder else 0)
        noise = [
            _noise_tool_descriptor(next_noise_index + j) for j in range(n_noise)
        ]
        next_noise_index += n_noise
        parts.append({"server_index": i, "initialize": INITIALIZE_RESPONSE})
        parts.append(
            {
                "server_index": i,
                "tools_list": {"tools": relevant + noise},
            }
        )
    return json.dumps(parts, separators=(",", ":"))


def acp_context_payload(manifest_json: str) -> str:
    """The ACP equivalent: a single ExecutionManifest body."""
    return manifest_json


__all__ = [
    "CATALOG",
    "INITIALIZE_RESPONSE",
    "ToolDescriptor",
    "tools_list_payload",
    "mcp_context_payload",
    "acp_context_payload",
]
