/*
Copyright 2026 Clawdlinux.

Licensed under the Business Source License 1.1.
See LICENSE in the repository root.
*/

// Package mcpclient contains small MCP client transports used by acp-bridge
// to call downstream MCP servers.
package mcpclient

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	mcpsource "github.com/Clawdlinux/agent-native-format/internal/sources/mcp"
)

const defaultProtocolVersion = "2025-03-26"

// ToolCallResult is the raw JSON-RPC result from a downstream MCP tools/call.
type ToolCallResult json.RawMessage

// Client forwards MCP requests to one downstream server.
type Client interface {
	Initialize(ctx context.Context) error
	ListTools(ctx context.Context) ([]mcpsource.ToolDescriptor, error)
	CallTool(ctx context.Context, name string, args map[string]interface{}) (json.RawMessage, error)
	Close() error
}

// StdioConfig describes a local stdio MCP server process.
type StdioConfig struct {
	Command string
	Args    []string
	Env     map[string]string
}

// StdioClient is a minimal JSON-RPC MCP client over newline-delimited stdio.
type StdioClient struct {
	reader *bufio.Reader
	writer io.Writer
	closer io.Closer
	cmd    *exec.Cmd

	mu     sync.Mutex
	nextID int64
}

// NewStdioClient constructs a client over an existing reader/writer pair.
// Tests use this path to avoid spawning processes.
func NewStdioClient(reader io.Reader, writer io.Writer, closer io.Closer) *StdioClient {
	return &StdioClient{reader: bufio.NewReader(reader), writer: writer, closer: closer}
}

// Spawn starts a local stdio MCP server process.
func Spawn(ctx context.Context, cfg StdioConfig) (*StdioClient, error) {
	command := strings.TrimSpace(cfg.Command)
	if command == "" {
		return nil, errors.New("mcpclient: stdio command is required")
	}

	cmd := exec.CommandContext(ctx, command, cfg.Args...)
	cmd.Env = mergeEnv(cfg.Env)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("mcpclient: stderr pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("mcpclient: stdout pipe: %w", err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("mcpclient: stdin pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mcpclient: start %s: %w", command, err)
	}

	go io.Copy(io.Discard, stderr) // avoid blocking noisy servers

	return &StdioClient{
		reader: bufio.NewReader(stdout),
		writer: stdin,
		closer: stdin,
		cmd:    cmd,
	}, nil
}

// Initialize performs the MCP initialize handshake and sends initialized.
func (c *StdioClient) Initialize(ctx context.Context) error {
	params := map[string]interface{}{
		"protocolVersion": defaultProtocolVersion,
		"capabilities":    map[string]interface{}{},
		"clientInfo": map[string]interface{}{
			"name":    "acp-bridge",
			"version": "0.2.0",
		},
	}
	if _, err := c.request(ctx, "initialize", params); err != nil {
		return err
	}
	return c.notify("notifications/initialized", nil)
}

// ListTools fetches the downstream tools/list result.
func (c *StdioClient) ListTools(ctx context.Context) ([]mcpsource.ToolDescriptor, error) {
	result, err := c.request(ctx, "tools/list", map[string]interface{}{})
	if err != nil {
		return nil, err
	}
	var body mcpsource.ToolsListResponse
	if err := json.Unmarshal(result, &body); err != nil {
		return nil, fmt.Errorf("mcpclient: decode tools/list: %w", err)
	}
	return body.Tools, nil
}

// CallTool forwards a downstream tools/call request and returns its raw result.
func (c *StdioClient) CallTool(ctx context.Context, name string, args map[string]interface{}) (json.RawMessage, error) {
	params := map[string]interface{}{
		"name":      name,
		"arguments": args,
	}
	return c.request(ctx, "tools/call", params)
}

// Close closes pipes and terminates a spawned process, if present.
func (c *StdioClient) Close() error {
	if c.closer != nil {
		_ = c.closer.Close()
	}
	if c.cmd == nil || c.cmd.Process == nil {
		return nil
	}
	_ = c.cmd.Process.Kill()
	return c.cmd.Wait()
}

func (c *StdioClient) request(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	id := atomic.AddInt64(&c.nextID, 1)
	if err := c.write(jsonrpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}); err != nil {
		return nil, err
	}

	for {
		line, err := readLine(ctx, c.reader)
		if err != nil {
			return nil, err
		}
		var resp jsonrpcResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			continue
		}
		if resp.ID == nil || string(resp.ID) != fmt.Sprintf("%d", id) {
			continue
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("mcpclient: %s failed: %s", method, resp.Error.Message)
		}
		return resp.Result, nil
	}
}

func (c *StdioClient) notify(method string, params interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.write(jsonrpcNotification{JSONRPC: "2.0", Method: method, Params: params})
}

func (c *StdioClient) write(v interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("mcpclient: marshal request: %w", err)
	}
	data = append(data, '\n')
	if _, err := c.writer.Write(data); err != nil {
		return fmt.Errorf("mcpclient: write request: %w", err)
	}
	return nil
}

func readLine(ctx context.Context, reader *bufio.Reader) ([]byte, error) {
	type result struct {
		line []byte
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		line, err := reader.ReadBytes('\n')
		ch <- result{line: line, err: err}
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-ch:
		if res.err != nil {
			return nil, fmt.Errorf("mcpclient: read response: %w", res.err)
		}
		return res.line, nil
	}
}

func mergeEnv(overrides map[string]string) []string {
	env := os.Environ()
	keys := make([]string, 0, len(overrides))
	for key := range overrides {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		env = append(env, key+"="+overrides[key])
	}
	return env
}

type jsonrpcRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int64       `json:"id,omitempty"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type jsonrpcNotification struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Manager routes bridge calls to source-specific clients.
type Manager struct {
	mu      sync.RWMutex
	clients map[string]Client
	timeout time.Duration
}

// NewManager constructs an empty downstream client manager.
func NewManager(timeout time.Duration) *Manager {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Manager{clients: make(map[string]Client), timeout: timeout}
}

// Register adds a downstream client under a source name.
func (m *Manager) Register(source string, client Client) error {
	if strings.TrimSpace(source) == "" {
		return errors.New("mcpclient: source name is required")
	}
	if client == nil {
		return errors.New("mcpclient: client is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clients[source] = client
	return nil
}

// Count returns the number of registered downstream clients.
func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.clients)
}

// Call forwards a tool call to the registered source client.
func (m *Manager) Call(ctx context.Context, source, tool string, args map[string]interface{}) (json.RawMessage, error) {
	m.mu.RLock()
	client, ok := m.clients[source]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("mcpclient: no downstream client for source %q", source)
	}
	callCtx, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()
	return client.CallTool(callCtx, tool, args)
}

// Close closes every registered client.
func (m *Manager) Close() error {
	m.mu.RLock()
	clients := make([]Client, 0, len(m.clients))
	for _, client := range m.clients {
		clients = append(clients, client)
	}
	m.mu.RUnlock()

	var errs []error
	for _, client := range clients {
		if err := client.Close(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
