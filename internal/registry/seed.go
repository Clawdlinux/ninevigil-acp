/*
Copyright 2026 Clawdlinux.

Licensed under the Business Source License 1.1.
See LICENSE in the repository root.
*/

package registry

import "github.com/Clawdlinux/agent-native-format/pkg/manifest"

// Seed populates a registry with a baseline of demo tools used by the Week 1
// PoC server, the docker-compose dev stack, and the benchmark harness.
//
// The set covers the capability tags exercised by scenarios S1-S3:
//   - sql      -> db.query
//   - messaging-> slack.send_message
//   - audit    -> audit.log_event
//   - template -> template.render
//   - email    -> email.send
//
// All schemas are emitted in the compact mini-language defined in SPEC §4.4.
func Seed(r Registry) error {
	tools := []Tool{
		{
			ID:           "db.query",
			Type:         "http",
			Endpoint:     "grpc://db-proxy.svc:50051/query",
			Method:       "POST",
			Schema:       map[string]string{"sql": "string", "limit": "int?"},
			Auth:         manifest.AuthPreInjected,
			Timeout:      "30s",
			Capabilities: []string{"sql", "database"},
			Egress:       []string{"db-proxy.svc"},
		},
		{
			ID:            "template.render",
			Type:          "http",
			Endpoint:      "http://template-svc.svc:8080/render",
			Method:        "POST",
			Schema:        map[string]string{"template_id": "string", "data": "json"},
			Auth:          manifest.AuthPreInjected,
			Timeout:       "10s",
			Capabilities:  []string{"template"},
			Egress:        []string{"template-svc.svc"},
			DependsOnCaps: []string{"sql"},
		},
		{
			ID:            "email.send",
			Type:          "http",
			Endpoint:      "https://email-gw.svc:443/send",
			Method:        "POST",
			Schema:        map[string]string{"to": "string[]", "subject": "string", "body": "string", "attachment_ref": "string?"},
			Auth:          manifest.AuthPreInjected,
			Timeout:       "15s",
			Capabilities:  []string{"email"},
			Egress:        []string{"email-gw.svc"},
			DependsOnCaps: []string{"template", "sql"},
			RequireApprov: true,
		},
		{
			ID:           "slack.send_message",
			Type:         "http",
			Endpoint:     "https://slack-gw.svc:443/messages",
			Method:       "POST",
			Schema:       map[string]string{"channel": "string", "text": "string"},
			Auth:         manifest.AuthPreInjected,
			Timeout:      "10s",
			Capabilities: []string{"messaging", "slack"},
			Egress:       []string{"slack-gw.svc"},
		},
		{
			ID:           "audit.log_event",
			Type:         "http",
			Endpoint:     "http://audit.svc:8080/events",
			Method:       "POST",
			Schema:       map[string]string{"actor": "string", "action": "string", "payload": "json"},
			Auth:         manifest.AuthPreInjected,
			Timeout:      "5s",
			Capabilities: []string{"audit"},
			Egress:       []string{"audit.svc"},
		},
	}

	for _, t := range tools {
		if err := r.Register(t); err != nil {
			return err
		}
	}
	return nil
}
