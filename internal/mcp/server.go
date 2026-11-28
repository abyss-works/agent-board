// Package mcp serves the agent-facing tools over the Model Context Protocol on
// stdio. Every tool is a thin call to the HTTP API through the client package,
// so a tool and the matching CLI command share one contract.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/wsk0715/agent-board/internal/client"
)

// ProtocolVersion is the protocol revision the server answers with.
const ProtocolVersion = "2025-06-18"

// maxMessageBytes bounds one JSON-RPC line. A spec body is at most 2 MiB and
// JSON escaping can grow it, so the cap leaves headroom above that.
const maxMessageBytes = 8 << 20

// Server dispatches JSON-RPC requests to the tools.
type Server struct {
	Client  *client.Client
	Version string
	tools   []tool
	byName  map[string]tool
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// New builds a server whose tools call c.
func New(c *client.Client, version string) *Server {
	s := &Server{Client: c, Version: version, byName: map[string]tool{}}
	s.tools = buildTools(c)
	for _, t := range s.tools {
		s.byName[t.Name] = t
	}
	return s
}

// Serve reads newline-delimited JSON-RPC messages from in and writes the
// replies to out until in ends or ctx is done. Requests are handled one at a
// time in arrival order.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64*1024), maxMessageBytes)
	var mu sync.Mutex
	write := func(r response) {
		r.JSONRPC = "2.0"
		b, err := json.Marshal(r)
		if err != nil {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		_, _ = out.Write(append(b, '\n'))
	}
	for sc.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var req request
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			write(response{ID: json.RawMessage("null"), Error: &rpcError{Code: -32700, Message: "parse error"}})
			continue
		}
		isNotification := len(req.ID) == 0
		result, rerr := s.handle(ctx, req)
		if isNotification {
			continue
		}
		write(response{ID: req.ID, Result: result, Error: rerr})
	}
	if err := sc.Err(); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func (s *Server) handle(ctx context.Context, req request) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		return map[string]any{
			"protocolVersion": ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "agb", "version": s.Version},
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		list := make([]map[string]any, 0, len(s.tools))
		for _, t := range s.tools {
			list = append(list, map[string]any{"name": t.Name, "description": t.Description, "inputSchema": t.Schema})
		}
		return map[string]any{"tools": list}, nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &rpcError{Code: -32602, Message: "invalid params"}
		}
		t, ok := s.byName[p.Name]
		if !ok {
			return nil, &rpcError{Code: -32602, Message: "unknown tool: " + p.Name}
		}
		args := map[string]any{}
		if len(p.Arguments) > 0 && string(p.Arguments) != "null" {
			if err := json.Unmarshal(p.Arguments, &args); err != nil {
				return toolError("arguments must be a JSON object"), nil
			}
		}
		body, err := t.Run(ctx, args)
		if err != nil {
			return toolError(describe(err)), nil
		}
		return map[string]any{"content": []map[string]any{{"type": "text", "text": string(body)}}}, nil
	}
	if strings.HasPrefix(req.Method, "notifications/") {
		return nil, nil
	}
	return nil, &rpcError{Code: -32601, Message: "method not found: " + req.Method}
}

func toolError(msg string) map[string]any {
	return map[string]any{"isError": true, "content": []map[string]any{{"type": "text", "text": msg}}}
}

// describe renders a tool failure for the agent: the server's error code and
// message, with field details when it sent them.
func describe(err error) string {
	var ae *client.Error
	if errors.As(err, &ae) {
		msg := fmt.Sprintf("%d %s: %s", ae.Status, ae.Code, ae.Message)
		for _, d := range ae.Details {
			msg += fmt.Sprintf("\n- %s %s %s", d.Field, d.Reason, d.Message)
		}
		return msg
	}
	if errors.Is(err, errBadArgument) {
		return err.Error()
	}
	return "request failed"
}
