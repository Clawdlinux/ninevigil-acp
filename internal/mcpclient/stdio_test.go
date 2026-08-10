/*
Copyright 2026 Clawdlinux.

Licensed under the Business Source License 1.1.
See LICENSE in the repository root.
*/

package mcpclient

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"strings"
	"testing"
)

func TestStdioClient_ForwardsToolsListAndCall(t *testing.T) {
	t.Parallel()

	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() { _ = clientConn.Close(); _ = serverConn.Close() })

	go serveFakeMCP(t, serverConn)

	client := NewStdioClient(clientConn, clientConn, clientConn)
	ctx := context.Background()

	if err := client.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools = %#v, want echo", tools)
	}

	result, err := client.CallTool(ctx, "echo", map[string]interface{}{"message": "hello"})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if !strings.Contains(string(result), "echo:hello") {
		t.Fatalf("result = %s, want echo output", result)
	}
}

func serveFakeMCP(t *testing.T, conn net.Conn) {
	t.Helper()
	reader := bufio.NewReader(conn)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF || strings.Contains(err.Error(), "closed") {
				return
			}
			t.Errorf("read request: %v", err)
			return
		}
		var req struct {
			ID     json.RawMessage        `json:"id"`
			Method string                 `json:"method"`
			Params map[string]interface{} `json:"params"`
		}
		if err := json.Unmarshal(line, &req); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		if len(req.ID) == 0 {
			continue
		}

		var result interface{}
		switch req.Method {
		case "initialize":
			result = map[string]interface{}{"protocolVersion": defaultProtocolVersion}
		case "tools/list":
			writeNotification(t, conn)
			result = map[string]interface{}{
				"tools": []map[string]interface{}{
					{
						"name":        "echo",
						"description": "Echo a message",
						"inputSchema": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"message": map[string]interface{}{"type": "string"},
							},
							"required": []interface{}{"message"},
						},
					},
				},
			}
		case "tools/call":
			args, _ := req.Params["arguments"].(map[string]interface{})
			result = map[string]interface{}{
				"content": []map[string]interface{}{
					{"type": "text", "text": "echo:" + args["message"].(string)},
				},
				"isError": false,
			}
		default:
			result = map[string]interface{}{}
		}
		writeResponse(t, conn, req.ID, result)
	}
}

func writeNotification(t *testing.T, writer io.Writer) {
	t.Helper()
	data, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "notifications/progress",
	})
	_, _ = writer.Write(append(data, '\n'))
}

func writeResponse(t *testing.T, writer io.Writer, id json.RawMessage, result interface{}) {
	t.Helper()
	data, err := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      json.RawMessage(id),
		"result":  result,
	})
	if err != nil {
		t.Errorf("marshal response: %v", err)
		return
	}
	_, _ = writer.Write(append(data, '\n'))
}
