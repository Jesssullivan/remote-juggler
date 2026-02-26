package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"
)

// ApertureWebhookReceiver handles real-time usage events from Aperture webhooks.
// Events are parsed and stored in a ring buffer, and optionally merged into
// the MeterStore for combined MCP + LLM metrics.
//
// Supports two payload formats:
//  1. Aperture hook events (real): {metadata: {model, login_name, ...}, tool_calls: [...], response_body: {usage: ...}}
//  2. Simple events (testing):     {type: "llm_call", agent: "...", model: "...", input_tokens: N, ...}
type ApertureWebhookReceiver struct {
	mu            sync.Mutex
	events        []ApertureEvent
	maxSize       int
	meter         *MeterStore
	webhookSecret string // If set, validates X-Webhook-Secret header.
}

// ApertureEvent is the normalized internal representation of a webhook event.
type ApertureEvent struct {
	Type         string          `json:"type"`
	Timestamp    time.Time       `json:"timestamp"`
	Agent        string          `json:"agent,omitempty"`
	CampaignID   string          `json:"campaign_id,omitempty"`
	Model        string          `json:"model,omitempty"`
	Provider     string          `json:"provider,omitempty"`
	Tokens       int             `json:"tokens,omitempty"`
	InputTokens  int             `json:"input_tokens,omitempty"`
	OutputTokens int             `json:"output_tokens,omitempty"`
	DurationMs   int64           `json:"duration_ms,omitempty"`
	Error        string          `json:"error,omitempty"`
	ToolNames    []string        `json:"tool_names,omitempty"`
	RequestID    string          `json:"request_id,omitempty"`
	SessionID    string          `json:"session_id,omitempty"`
	Raw          json.RawMessage `json:"raw,omitempty"`
}

// apertureHookPayload is the real Aperture webhook format.
type apertureHookPayload struct {
	Metadata struct {
		LoginName    string `json:"login_name"`
		UserAgent    string `json:"user_agent"`
		URL          string `json:"url"`
		Model        string `json:"model"`
		Provider     string `json:"provider"`
		TailnetName  string `json:"tailnet_name"`
		StableNodeID string `json:"stable_node_id"`
		RequestID    string `json:"request_id"`
		SessionID    string `json:"session_id"`
	} `json:"metadata"`
	ToolCalls []struct {
		Name   string          `json:"name"`
		Params json.RawMessage `json:"params"`
	} `json:"tool_calls"`
	ResponseBody json.RawMessage `json:"response_body"`
}

// apertureResponseUsage extracts token counts from Anthropic-style response body.
type apertureResponseUsage struct {
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// NewApertureWebhookReceiver creates a webhook receiver with the given ring buffer size.
// If webhookSecret is non-empty, incoming requests must include a matching X-Webhook-Secret header.
func NewApertureWebhookReceiver(maxSize int, meter *MeterStore, webhookSecret string) *ApertureWebhookReceiver {
	if maxSize <= 0 {
		maxSize = 1000
	}
	return &ApertureWebhookReceiver{
		events:        make([]ApertureEvent, 0, maxSize),
		maxSize:       maxSize,
		meter:         meter,
		webhookSecret: webhookSecret,
	}
}

// HandleWebhook is the HTTP handler for POST /aperture/webhook.
func (r *ApertureWebhookReceiver) HandleWebhook(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Validate webhook secret if configured.
	if r.webhookSecret != "" {
		if req.Header.Get("X-Webhook-Secret") != r.webhookSecret {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}

	body, err := io.ReadAll(req.Body)
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}

	events, err := r.parsePayload(body)
	if err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	accepted := 0
	for _, event := range events {
		if event.Type == "" {
			continue
		}
		if event.Timestamp.IsZero() {
			event.Timestamp = time.Now().UTC()
		}
		r.record(event)
		accepted++
	}

	log.Printf("aperture webhook: accepted %d/%d events", accepted, len(events))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"accepted": accepted,
		"total":    len(events),
	})
}

// parsePayload handles both Aperture hook format and simple event format.
func (r *ApertureWebhookReceiver) parsePayload(body []byte) ([]ApertureEvent, error) {
	// Try Aperture hook format first (has "metadata" key).
	if event, ok := r.tryApertureHook(body); ok {
		return []ApertureEvent{event}, nil
	}

	// Try array of simple events.
	var events []ApertureEvent
	if err := json.Unmarshal(body, &events); err == nil && len(events) > 0 {
		for i := range events {
			events[i].Raw = body
		}
		return events, nil
	}

	// Try single simple event.
	var single ApertureEvent
	if err := json.Unmarshal(body, &single); err != nil {
		return nil, fmt.Errorf("unrecognized payload: %w", err)
	}
	single.Raw = body
	return []ApertureEvent{single}, nil
}

// tryApertureHook attempts to parse as a real Aperture hook payload.
func (r *ApertureWebhookReceiver) tryApertureHook(body []byte) (ApertureEvent, bool) {
	var payload apertureHookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return ApertureEvent{}, false
	}

	// Must have metadata with a model to be recognized as an Aperture hook.
	if payload.Metadata.Model == "" {
		return ApertureEvent{}, false
	}

	event := ApertureEvent{
		Type:      "llm_call",
		Model:     payload.Metadata.Model,
		Provider:  payload.Metadata.Provider,
		Agent:     payload.Metadata.LoginName,
		RequestID: payload.Metadata.RequestID,
		SessionID: payload.Metadata.SessionID,
		Raw:       body,
	}

	// Extract tool names.
	for _, tc := range payload.ToolCalls {
		if tc.Name != "" {
			event.ToolNames = append(event.ToolNames, tc.Name)
		}
	}

	// Extract token counts from response body.
	if len(payload.ResponseBody) > 0 {
		var respUsage apertureResponseUsage
		if err := json.Unmarshal(payload.ResponseBody, &respUsage); err == nil {
			event.InputTokens = respUsage.Usage.InputTokens
			event.OutputTokens = respUsage.Usage.OutputTokens
		}
	}

	log.Printf("aperture webhook: parsed hook event model=%s agent=%s tools=%v input_tokens=%d output_tokens=%d session=%s",
		event.Model, event.Agent, event.ToolNames, event.InputTokens, event.OutputTokens, event.SessionID)

	return event, true
}

// record adds an event to the ring buffer and optionally updates MeterStore.
func (r *ApertureWebhookReceiver) record(event ApertureEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Ring buffer eviction.
	if len(r.events) >= r.maxSize {
		r.events = r.events[1:]
	}
	r.events = append(r.events, event)

	// Merge LLM-layer metrics into MeterStore if available.
	if r.meter != nil && event.Type == "llm_call" {
		inputTok := event.InputTokens
		outputTok := event.OutputTokens
		// Backward compat: if only "tokens" is set, attribute to output.
		if inputTok == 0 && outputTok == 0 && event.Tokens > 0 {
			outputTok = event.Tokens
		}
		// Build dedup key matching SSE ingester format so the same LLM call
		// reported by both sources is only counted once.
		dedupeKey := fmt.Sprintf("%s:%s:%d:%d:%d:%d",
			event.Model, event.Agent, inputTok, outputTok, event.DurationMs, event.Timestamp.Unix())
		r.meter.Record(MeterRecord{
			Agent:        event.Agent,
			CampaignID:   event.CampaignID,
			ToolName:     fmt.Sprintf("llm:%s", event.Model),
			DurationMs:   event.DurationMs,
			Timestamp:    event.Timestamp,
			IsError:      event.Error != "",
			InputTokens:  inputTok,
			OutputTokens: outputTok,
			DedupeKey:    dedupeKey,
		})
	}
}

// Recent returns the last n events (newest first).
func (r *ApertureWebhookReceiver) Recent(n int) []ApertureEvent {
	r.mu.Lock()
	defer r.mu.Unlock()

	if n > len(r.events) {
		n = len(r.events)
	}

	result := make([]ApertureEvent, n)
	for i := 0; i < n; i++ {
		result[i] = r.events[len(r.events)-1-i]
	}
	return result
}

// Count returns the total number of events in the buffer.
func (r *ApertureWebhookReceiver) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.events)
}
