package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
	"sync"
	"time"
)

// MCPProxy manages a Chapel MCP subprocess and bridges STDIO to HTTP/SSE.
// It intercepts tools/list to inject gateway tools and dispatches gateway
// tool calls locally without forwarding to the subprocess.
type MCPProxy struct {
	binaryPath string

	mu    sync.Mutex
	cmd   *exec.Cmd
	stdin io.WriteCloser

	// responseCh receives JSON-RPC responses (messages with "id") from readLoop.
	responseCh chan []byte

	// SSE subscribers receive JSON-RPC notifications from the subprocess.
	subs   map[chan []byte]struct{}
	subsMu sync.Mutex

	// Gateway tool handlers (set after construction).
	resolver   *Resolver
	setec      *SetecClient
	audit      *AuditLog
	aperture   *ApertureClient
	meterStore *MeterStore
	github     *GitHubToolHandler
	campaigns  *CampaignClient
}

// NewMCPProxy creates a proxy for the given Chapel binary.
func NewMCPProxy(binaryPath string) *MCPProxy {
	return &MCPProxy{
		binaryPath: binaryPath,
		responseCh: make(chan []byte, 1),
		subs:       make(map[chan []byte]struct{}),
	}
}

// Start launches the Chapel MCP subprocess and sends the MCP initialize
// handshake so the subprocess is ready to handle tool calls.
func (p *MCPProxy) Start() error {
	p.mu.Lock()

	cmd := exec.Command(p.binaryPath, "--mode=mcp")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		p.mu.Unlock()
		return fmt.Errorf("stdin pipe: %w", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		p.mu.Unlock()
		return fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		p.mu.Unlock()
		return fmt.Errorf("start subprocess: %w", err)
	}

	p.cmd = cmd
	p.stdin = stdin

	// readLoop is the sole reader from stdout. It routes responses to
	// responseCh and broadcasts notifications to SSE subscribers.
	go p.readLoop(bufio.NewReader(stdoutPipe))

	p.mu.Unlock()

	// Send MCP initialize handshake so the subprocess is ready for tool calls.
	initReq, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      0,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]any{},
			"clientInfo": map[string]any{
				"name":    "rj-gateway",
				"version": "2.3.0",
			},
		},
	})
	resp, err := p.SendRequest(initReq)
	if err != nil {
		log.Printf("warning: mcp initialize failed: %v", err)
	} else {
		log.Printf("mcp: subprocess initialized (%d bytes response)", len(resp))
	}

	return nil
}

// Stop terminates the subprocess.
func (p *MCPProxy) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.stdin != nil {
		p.stdin.Close()
	}
	if p.cmd != nil && p.cmd.Process != nil {
		return p.cmd.Process.Kill()
	}
	return nil
}

// readLoop is the sole reader from the subprocess stdout. It routes
// JSON-RPC responses (messages with "id") to responseCh and broadcasts
// notifications (messages without "id") to SSE subscribers.
func (p *MCPProxy) readLoop(stdout *bufio.Reader) {
	for {
		line, err := stdout.ReadBytes('\n')
		if err != nil {
			if err != io.EOF {
				log.Printf("mcp subprocess read error: %v", err)
			}
			close(p.responseCh)
			return
		}

		// Trim whitespace/BOM that Chapel may emit around JSON.
		// Also strip NUL bytes that Chapel's string handling may embed.
		line = bytes.TrimSpace(line)
		line = bytes.ReplaceAll(line, []byte{0}, nil)
		if len(line) == 0 {
			continue
		}

		var msg map[string]json.RawMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			log.Printf("mcp: skipping non-JSON line (%d bytes): %v", len(line), err)
			continue
		}

		if _, hasID := msg["id"]; hasID {
			// Response to a request — route to SendRequest waiter.
			p.responseCh <- line
		} else {
			// Notification — broadcast to SSE subscribers.
			p.broadcast(line)
		}
	}
}

// broadcast sends a message to all SSE subscribers.
func (p *MCPProxy) broadcast(data []byte) {
	p.subsMu.Lock()
	defer p.subsMu.Unlock()
	for ch := range p.subs {
		select {
		case ch <- data:
		default:
			// Drop if subscriber is slow.
		}
	}
}

// subscribe registers an SSE listener.
func (p *MCPProxy) subscribe() chan []byte {
	ch := make(chan []byte, 64)
	p.subsMu.Lock()
	p.subs[ch] = struct{}{}
	p.subsMu.Unlock()
	return ch
}

// unsubscribe removes an SSE listener.
func (p *MCPProxy) unsubscribe(ch chan []byte) {
	p.subsMu.Lock()
	delete(p.subs, ch)
	p.subsMu.Unlock()
	close(ch)
}

// SendRequest writes a JSON-RPC request to the subprocess and waits for
// the response from readLoop. Serialized: only one request at a time.
func (p *MCPProxy) SendRequest(request []byte) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Write request followed by newline.
	if _, err := p.stdin.Write(append(request, '\n')); err != nil {
		return nil, fmt.Errorf("write to subprocess: %w", err)
	}

	// Wait for response from readLoop.
	line, ok := <-p.responseCh
	if !ok {
		return nil, fmt.Errorf("subprocess closed")
	}

	return line, nil
}

// gatewayToolNames are tools handled by the gateway, not the Chapel subprocess.
var gatewayToolNames = map[string]bool{
	"juggler_resolve_composite": true,
	"juggler_setec_list":        true,
	"juggler_setec_get":         true,
	"juggler_setec_put":         true,
	"juggler_audit_log":         true,
	"juggler_campaign_status":   true,
	"juggler_aperture_usage":    true,
	"github_fetch":              true,
	"github_list_alerts":        true,
	"github_get_alert":          true,
	"github_create_branch":      true,
	"github_update_file":        true,
	"github_patch_file":         true,
	"github_create_pr":          true,
	"github_create_issue":       true,
	"juggler_request_secret":    true,
	"juggler_campaign_trigger":  true,
	"juggler_campaign_list":     true,
}

// HandleRPC handles POST /mcp JSON-RPC requests.
// It intercepts tools/list to inject gateway tools and dispatches
// gateway-specific tool calls locally.
func (p *MCPProxy) HandleRPC(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Parse the request to check if it's a gateway-handled tool call.
	var req struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Params  struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		} `json:"params"`
	}
	if err := json.Unmarshal(body, &req); err == nil {
		// Check if this is a tools/call for a gateway tool.
		if req.Method == "tools/call" && gatewayToolNames[req.Params.Name] {
			start := time.Now()
			resp := p.handleGatewayTool(req.ID, req.Params.Name, req.Params.Arguments, r)
			if p.meterStore != nil {
				agent, campaignID := extractMeteringContext(req.Params.Arguments)
				p.meterStore.Record(MeterRecord{
					Agent:         agent,
					CampaignID:    campaignID,
					ToolName:      req.Params.Name,
					RequestBytes:  len(body),
					ResponseBytes: len(resp),
					DurationMs:    time.Since(start).Milliseconds(),
					Timestamp:     start,
					IsError:       bytes.Contains(resp, []byte(`"error"`)),
				})
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write(resp)
			return
		}

		// Record metering for Chapel-proxied tool calls.
		if req.Method == "tools/call" && p.meterStore != nil {
			start := time.Now()
			resp, err := p.SendRequest(body)
			if err != nil {
				http.Error(w, "proxy error: "+err.Error(), http.StatusBadGateway)
				return
			}
			agent, campaignID := extractMeteringContext(req.Params.Arguments)
			p.meterStore.Record(MeterRecord{
				Agent:         agent,
				CampaignID:    campaignID,
				ToolName:      req.Params.Name,
				RequestBytes:  len(body),
				ResponseBytes: len(resp),
				DurationMs:    time.Since(start).Milliseconds(),
				Timestamp:     start,
				IsError:       bytes.Contains(resp, []byte(`"error"`)),
			})
			w.Header().Set("Content-Type", "application/json")
			w.Write(resp)
			return
		}
	}

	// Forward to Chapel subprocess (non-tool calls: initialize, tools/list, etc.).
	resp, err := p.SendRequest(body)
	if err != nil {
		// If Chapel is down and this was a tools/list request, return gateway-only tools.
		if req.Method == "tools/list" {
			resp = gatewayOnlyToolsList(req.ID)
			w.Header().Set("Content-Type", "application/json")
			w.Write(resp)
			return
		}
		http.Error(w, "proxy error: "+err.Error(), http.StatusBadGateway)
		return
	}

	// Intercept tools/list responses to inject gateway tools.
	if isToolsListResponse(resp) {
		resp = injectGatewayTools(resp)
	} else if req.Method == "tools/list" {
		// Chapel returned an error for tools/list — return gateway tools only.
		resp = gatewayOnlyToolsList(req.ID)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(resp)
}

// handleGatewayTool dispatches a tool call to the gateway's local handlers.
func (p *MCPProxy) handleGatewayTool(id json.RawMessage, tool string, args json.RawMessage, r *http.Request) []byte {
	caller, _ := IdentityFromContext(r.Context())
	ctx := r.Context()

	var result json.RawMessage
	var err error

	switch tool {
	case "juggler_resolve_composite":
		result, err = handleResolveAsMCPTool(ctx, p.resolver, args)
		if p.audit != nil {
			var a struct{ Query string }
			json.Unmarshal(args, &a)
			p.audit.Log(AuditEntry{
				Caller:  caller,
				Action:  "resolve_composite",
				Query:   a.Query,
				Allowed: err == nil,
			})
		}

	case "juggler_setec_list":
		names, e := p.setec.List(ctx)
		if e != nil {
			err = e
		} else {
			result, _ = json.Marshal(map[string]any{
				"content": []map[string]any{{
					"type": "text",
					"text": mustMarshal(map[string]any{"secrets": names}),
				}},
			})
		}

	case "juggler_setec_get":
		var a struct{ Name string }
		json.Unmarshal(args, &a)
		val, e := p.setec.Get(ctx, a.Name)
		if e != nil {
			err = e
		} else {
			result, _ = json.Marshal(map[string]any{
				"content": []map[string]any{{
					"type": "text",
					"text": val,
				}},
			})
		}
		if p.audit != nil {
			p.audit.Log(AuditEntry{
				Caller:  caller,
				Action:  "setec_get",
				Query:   a.Name,
				Source:  "setec",
				Allowed: e == nil,
			})
		}

	case "juggler_setec_put":
		var a struct {
			Name  string
			Value string
		}
		json.Unmarshal(args, &a)
		e := p.setec.Put(ctx, a.Name, a.Value)
		if e != nil {
			err = e
		} else {
			result, _ = json.Marshal(map[string]any{
				"content": []map[string]any{{
					"type": "text",
					"text": fmt.Sprintf("stored secret %q in setec", a.Name),
				}},
			})
		}

	case "juggler_audit_log":
		var a struct{ Count int }
		json.Unmarshal(args, &a)
		if a.Count <= 0 {
			a.Count = 20
		}
		if a.Count > 100 {
			a.Count = 100
		}
		entries := p.audit.Recent(a.Count)
		result, _ = json.Marshal(map[string]any{
			"content": []map[string]any{{
				"type": "text",
				"text": mustMarshal(map[string]any{"entries": entries, "count": len(entries)}),
			}},
		})

	case "juggler_campaign_status":
		result, err = handleCampaignStatusTool(p.setec, args)

	case "juggler_aperture_usage":
		result, err = handleApertureUsageTool(p.aperture, args)

	case "juggler_campaign_trigger":
		result, err = handleCampaignTriggerTool(p.campaigns, args)
		if p.audit != nil {
			var a struct {
				CampaignID string `json:"campaign_id"`
			}
			json.Unmarshal(args, &a)
			p.audit.Log(AuditEntry{
				Caller:  caller,
				Action:  "campaign_trigger",
				Query:   a.CampaignID,
				Allowed: err == nil,
			})
		}

	case "juggler_campaign_list":
		result, err = handleCampaignListTool(p.campaigns, args)

	case "github_fetch":
		result, err = p.github.Fetch(ctx, args)
		if p.audit != nil {
			var a struct{ Owner, Repo, Path string }
			json.Unmarshal(args, &a)
			p.audit.Log(AuditEntry{
				Caller:  caller,
				Action:  "github_fetch",
				Query:   fmt.Sprintf("%s/%s/%s", a.Owner, a.Repo, a.Path),
				Allowed: err == nil,
			})
		}

	case "github_list_alerts":
		result, err = p.github.ListAlerts(ctx, args)
		if p.audit != nil {
			var a struct{ Owner, Repo string }
			json.Unmarshal(args, &a)
			p.audit.Log(AuditEntry{
				Caller:  caller,
				Action:  "github_list_alerts",
				Query:   fmt.Sprintf("%s/%s", a.Owner, a.Repo),
				Allowed: err == nil,
			})
		}

	case "github_get_alert":
		result, err = p.github.GetAlert(ctx, args)
		if p.audit != nil {
			var a struct {
				Owner       string `json:"owner"`
				Repo        string `json:"repo"`
				AlertNumber int    `json:"alert_number"`
			}
			json.Unmarshal(args, &a)
			p.audit.Log(AuditEntry{
				Caller:  caller,
				Action:  "github_get_alert",
				Query:   fmt.Sprintf("%s/%s/%d", a.Owner, a.Repo, a.AlertNumber),
				Allowed: err == nil,
			})
		}

	case "github_create_branch":
		result, err = p.github.CreateBranch(ctx, args)
		if p.audit != nil {
			var a struct{ Owner, Repo, BranchName string }
			json.Unmarshal(args, &a)
			p.audit.Log(AuditEntry{
				Caller:  caller,
				Action:  "github_create_branch",
				Query:   fmt.Sprintf("%s/%s/%s", a.Owner, a.Repo, a.BranchName),
				Allowed: err == nil,
			})
		}

	case "github_update_file":
		result, err = p.github.UpdateFile(ctx, args)
		if p.audit != nil {
			var a struct{ Owner, Repo, Path, Branch string }
			json.Unmarshal(args, &a)
			p.audit.Log(AuditEntry{
				Caller:  caller,
				Action:  "github_update_file",
				Query:   fmt.Sprintf("%s/%s/%s@%s", a.Owner, a.Repo, a.Path, a.Branch),
				Allowed: err == nil,
			})
		}

	case "github_patch_file":
		result, err = p.github.PatchFile(ctx, args)
		if p.audit != nil {
			var a struct{ Owner, Repo, Path, Branch string }
			json.Unmarshal(args, &a)
			p.audit.Log(AuditEntry{
				Caller:  caller,
				Action:  "github_patch_file",
				Query:   fmt.Sprintf("%s/%s/%s@%s", a.Owner, a.Repo, a.Path, a.Branch),
				Allowed: err == nil,
			})
		}

	case "github_create_pr":
		result, err = p.github.CreatePR(ctx, args)
		if p.audit != nil {
			var a struct{ Owner, Repo, Head string }
			json.Unmarshal(args, &a)
			p.audit.Log(AuditEntry{
				Caller:  caller,
				Action:  "github_create_pr",
				Query:   fmt.Sprintf("%s/%s/%s", a.Owner, a.Repo, a.Head),
				Allowed: err == nil,
			})
		}

	case "github_create_issue":
		result, err = p.github.CreateIssue(ctx, args)
		if p.audit != nil {
			var a struct{ Owner, Repo, Title string }
			json.Unmarshal(args, &a)
			p.audit.Log(AuditEntry{
				Caller:  caller,
				Action:  "github_create_issue",
				Query:   fmt.Sprintf("%s/%s: %s", a.Owner, a.Repo, a.Title),
				Allowed: err == nil,
			})
		}

	case "juggler_request_secret":
		result, err = p.github.RequestSecret(ctx, args)
		if p.audit != nil {
			var a struct{ Name, Urgency string }
			json.Unmarshal(args, &a)
			p.audit.Log(AuditEntry{
				Caller:  caller,
				Action:  "juggler_request_secret",
				Query:   a.Name,
				Allowed: err == nil,
				Reason:  "urgency=" + a.Urgency,
			})
		}

	default:
		err = fmt.Errorf("unknown gateway tool: %s", tool)
	}

	if err != nil {
		resp, _ := json.Marshal(map[string]any{
			"jsonrpc": "2.0",
			"id":      id,
			"error":   map[string]any{"code": -32000, "message": err.Error()},
		})
		return resp
	}

	resp, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	})
	return resp
}

// HandleSSE handles GET /mcp/sse for Server-Sent Events.
func (p *MCPProxy) HandleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := p.subscribe()
	defer p.unsubscribe(ch)

	for {
		select {
		case <-r.Context().Done():
			return
		case data := <-ch:
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}
