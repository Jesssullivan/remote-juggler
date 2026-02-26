package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestWebhookSingleEvent(t *testing.T) {
	receiver := NewApertureWebhookReceiver(100, nil, "")

	body, _ := json.Marshal(ApertureEvent{
		Type:      "llm_call",
		Agent:     "openclaw",
		Model:     "claude-sonnet-4",
		Tokens:    1500,
		Timestamp: time.Now(),
	})

	req := httptest.NewRequest(http.MethodPost, "/aperture/webhook", bytes.NewReader(body))
	w := httptest.NewRecorder()
	receiver.HandleWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["accepted"].(float64) != 1 {
		t.Errorf("accepted = %v, want 1", resp["accepted"])
	}

	events := receiver.Recent(10)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != "llm_call" {
		t.Errorf("type = %q, want 'llm_call'", events[0].Type)
	}
}

func TestWebhookBatchEvents(t *testing.T) {
	receiver := NewApertureWebhookReceiver(100, nil, "")

	events := []ApertureEvent{
		{Type: "llm_call", Agent: "openclaw", Tokens: 100, Timestamp: time.Now()},
		{Type: "llm_call", Agent: "hexstrike", Tokens: 200, Timestamp: time.Now()},
		{Type: "rate_limit", Agent: "openclaw", Timestamp: time.Now()},
	}
	body, _ := json.Marshal(events)

	req := httptest.NewRequest(http.MethodPost, "/aperture/webhook", bytes.NewReader(body))
	w := httptest.NewRecorder()
	receiver.HandleWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["accepted"].(float64) != 3 {
		t.Errorf("accepted = %v, want 3", resp["accepted"])
	}

	if receiver.Count() != 3 {
		t.Errorf("count = %d, want 3", receiver.Count())
	}
}

func TestWebhookMalformedJSON(t *testing.T) {
	receiver := NewApertureWebhookReceiver(100, nil, "")

	req := httptest.NewRequest(http.MethodPost, "/aperture/webhook", bytes.NewReader([]byte("{invalid")))
	w := httptest.NewRecorder()
	receiver.HandleWebhook(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestWebhookMethodNotAllowed(t *testing.T) {
	receiver := NewApertureWebhookReceiver(100, nil, "")

	req := httptest.NewRequest(http.MethodGet, "/aperture/webhook", nil)
	w := httptest.NewRecorder()
	receiver.HandleWebhook(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", w.Code)
	}
}

func TestWebhookSkipsEventsWithoutType(t *testing.T) {
	receiver := NewApertureWebhookReceiver(100, nil, "")

	events := []ApertureEvent{
		{Type: "llm_call", Agent: "openclaw", Timestamp: time.Now()},
		{Type: "", Agent: "unknown", Timestamp: time.Now()}, // No type -- skip.
	}
	body, _ := json.Marshal(events)

	req := httptest.NewRequest(http.MethodPost, "/aperture/webhook", bytes.NewReader(body))
	w := httptest.NewRecorder()
	receiver.HandleWebhook(w, req)

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["accepted"].(float64) != 1 {
		t.Errorf("accepted = %v, want 1", resp["accepted"])
	}
}

func TestWebhookRingBufferEviction(t *testing.T) {
	receiver := NewApertureWebhookReceiver(3, nil, "")

	for i := 0; i < 5; i++ {
		receiver.record(ApertureEvent{
			Type:      "llm_call",
			Agent:     "openclaw",
			Tokens:    i + 1,
			Timestamp: time.Now(),
		})
	}

	if receiver.Count() != 3 {
		t.Errorf("count = %d, want 3 (buffer size)", receiver.Count())
	}

	events := receiver.Recent(3)
	// Newest first: tokens should be 5, 4, 3.
	if events[0].Tokens != 5 {
		t.Errorf("newest event tokens = %d, want 5", events[0].Tokens)
	}
	if events[2].Tokens != 3 {
		t.Errorf("oldest event tokens = %d, want 3", events[2].Tokens)
	}
}

func TestWebhookMergesIntoMeterStore(t *testing.T) {
	store := NewMeterStore()
	receiver := NewApertureWebhookReceiver(100, store, "")

	now := time.Now()
	receiver.record(ApertureEvent{
		Type:         "llm_call",
		Agent:        "openclaw",
		CampaignID:   "oc-dep-audit",
		Model:        "claude-sonnet-4",
		InputTokens:  100,
		OutputTokens: 50,
		DurationMs:   500,
		Timestamp:    now,
	})
	receiver.record(ApertureEvent{
		Type:         "llm_call",
		Agent:        "openclaw",
		CampaignID:   "oc-dep-audit",
		Model:        "claude-sonnet-4",
		InputTokens:  200,
		OutputTokens: 80,
		DurationMs:   300,
		Error:        "rate_limit",
		Timestamp:    now,
	})

	// Check MeterStore has the merged data.
	buckets := store.Query("openclaw", "oc-dep-audit")
	if len(buckets) != 1 {
		t.Fatalf("expected 1 bucket, got %d", len(buckets))
	}
	if buckets[0].ToolCalls != 2 {
		t.Errorf("ToolCalls = %d, want 2", buckets[0].ToolCalls)
	}
	if buckets[0].ErrorCount != 1 {
		t.Errorf("ErrorCount = %d, want 1", buckets[0].ErrorCount)
	}
	if buckets[0].TotalDurationMs != 800 {
		t.Errorf("TotalDurationMs = %d, want 800", buckets[0].TotalDurationMs)
	}
}

func TestWebhookMergesTokensIntoMeterStore(t *testing.T) {
	store := NewMeterStore()
	receiver := NewApertureWebhookReceiver(100, store, "")

	// Event with input/output tokens.
	receiver.record(ApertureEvent{
		Type:         "llm_call",
		Agent:        "openclaw",
		CampaignID:   "oc-smoketest",
		Model:        "claude-sonnet-4-20250514",
		InputTokens:  1200,
		OutputTokens: 350,
		Timestamp:    time.Now(),
	})
	// Event with legacy "tokens" field only.
	receiver.record(ApertureEvent{
		Type:       "llm_call",
		Agent:      "openclaw",
		CampaignID: "oc-smoketest",
		Model:      "claude-sonnet-4-20250514",
		Tokens:     500,
		Timestamp:  time.Now(),
	})

	buckets := store.Query("openclaw", "oc-smoketest")
	if len(buckets) != 1 {
		t.Fatalf("expected 1 bucket, got %d", len(buckets))
	}
	b := buckets[0]
	// First event: input=1200, output=350; second: legacy tokens=500 -> output=500
	if b.InputTokens != 1200 {
		t.Errorf("InputTokens = %d, want 1200", b.InputTokens)
	}
	if b.OutputTokens != 850 {
		t.Errorf("OutputTokens = %d, want 850 (350 + 500 legacy)", b.OutputTokens)
	}
}

func TestWebhookNonLLMEventNotMerged(t *testing.T) {
	store := NewMeterStore()
	receiver := NewApertureWebhookReceiver(100, store, "")

	receiver.record(ApertureEvent{
		Type:      "rate_limit",
		Agent:     "openclaw",
		Timestamp: time.Now(),
	})

	// Non-llm_call events should not create meter records.
	buckets := store.Query("openclaw", "")
	if len(buckets) != 0 {
		t.Errorf("expected 0 buckets for non-llm_call event, got %d", len(buckets))
	}
}

func TestWebhookApertureHookFormat(t *testing.T) {
	store := NewMeterStore()
	receiver := NewApertureWebhookReceiver(100, store, "")

	// Real Aperture hook payload format (matches what ai.taila4c78d.ts.net sends).
	payload := map[string]any{
		"metadata": map[string]any{
			"login_name":     "tagged-devices",
			"user_agent":     "Anthropic/Python 0.83.0",
			"url":            "http://aperture.fuzzy-dev.svc.cluster.local/v1/messages",
			"model":          "claude-sonnet-4-20250514",
			"provider":       "anthropic",
			"tailnet_name":   "taila4c78d.ts.net",
			"stable_node_id": "nvkZciDFvo11CNTRL",
			"request_id":     "req-123",
			"session_id":     "sess-456",
		},
		"tool_calls": []map[string]any{
			{"name": "juggler_setec_list", "params": map[string]any{}},
		},
		"response_body": map[string]any{
			"id":    "msg_abc",
			"type":  "message",
			"model": "claude-sonnet-4-20250514",
			"usage": map[string]any{
				"input_tokens":  2400,
				"output_tokens": 680,
			},
		},
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/aperture/webhook", bytes.NewReader(body))
	w := httptest.NewRecorder()
	receiver.HandleWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["accepted"].(float64) != 1 {
		t.Errorf("accepted = %v, want 1", resp["accepted"])
	}

	events := receiver.Recent(10)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	e := events[0]
	if e.Type != "llm_call" {
		t.Errorf("Type = %q, want 'llm_call'", e.Type)
	}
	if e.Model != "claude-sonnet-4-20250514" {
		t.Errorf("Model = %q, want 'claude-sonnet-4-20250514'", e.Model)
	}
	if e.Agent != "tagged-devices" {
		t.Errorf("Agent = %q, want 'tagged-devices'", e.Agent)
	}
	if e.Provider != "anthropic" {
		t.Errorf("Provider = %q, want 'anthropic'", e.Provider)
	}
	if e.InputTokens != 2400 {
		t.Errorf("InputTokens = %d, want 2400", e.InputTokens)
	}
	if e.OutputTokens != 680 {
		t.Errorf("OutputTokens = %d, want 680", e.OutputTokens)
	}
	if len(e.ToolNames) != 1 || e.ToolNames[0] != "juggler_setec_list" {
		t.Errorf("ToolNames = %v, want [juggler_setec_list]", e.ToolNames)
	}
	if e.RequestID != "req-123" {
		t.Errorf("RequestID = %q, want 'req-123'", e.RequestID)
	}

	// Verify tokens merged into MeterStore.
	buckets := store.Query("tagged-devices", "")
	if len(buckets) != 1 {
		t.Fatalf("expected 1 bucket, got %d", len(buckets))
	}
	if buckets[0].InputTokens != 2400 {
		t.Errorf("MeterStore InputTokens = %d, want 2400", buckets[0].InputTokens)
	}
	if buckets[0].OutputTokens != 680 {
		t.Errorf("MeterStore OutputTokens = %d, want 680", buckets[0].OutputTokens)
	}
}

func TestWebhookApertureHookNoResponseBody(t *testing.T) {
	receiver := NewApertureWebhookReceiver(100, nil, "")

	// Aperture hook with no response_body (e.g. streaming or error).
	payload := map[string]any{
		"metadata": map[string]any{
			"model":    "claude-sonnet-4-20250514",
			"provider": "anthropic",
		},
		"tool_calls": []map[string]any{},
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/aperture/webhook", bytes.NewReader(body))
	w := httptest.NewRecorder()
	receiver.HandleWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	events := receiver.Recent(10)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].InputTokens != 0 {
		t.Errorf("InputTokens = %d, want 0 (no response_body)", events[0].InputTokens)
	}
}

func TestWebhookRecentOrder(t *testing.T) {
	receiver := NewApertureWebhookReceiver(100, nil, "")

	for i := 0; i < 5; i++ {
		receiver.record(ApertureEvent{
			Type:      "llm_call",
			Tokens:    i + 1,
			Timestamp: time.Now(),
		})
	}

	events := receiver.Recent(3)
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	// Newest first.
	if events[0].Tokens != 5 {
		t.Errorf("events[0].Tokens = %d, want 5", events[0].Tokens)
	}
	if events[1].Tokens != 4 {
		t.Errorf("events[1].Tokens = %d, want 4", events[1].Tokens)
	}
	if events[2].Tokens != 3 {
		t.Errorf("events[2].Tokens = %d, want 3", events[2].Tokens)
	}
}

func TestWebhookSecretRejectsUnauthorized(t *testing.T) {
	receiver := NewApertureWebhookReceiver(100, nil, "my-secret-token")

	body, _ := json.Marshal(ApertureEvent{
		Type:  "llm_call",
		Agent: "openclaw",
	})

	// No secret header.
	req := httptest.NewRequest(http.MethodPost, "/aperture/webhook", bytes.NewReader(body))
	w := httptest.NewRecorder()
	receiver.HandleWebhook(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 (no secret header)", w.Code)
	}

	// Wrong secret.
	req = httptest.NewRequest(http.MethodPost, "/aperture/webhook", bytes.NewReader(body))
	req.Header.Set("X-Webhook-Secret", "wrong-secret")
	w = httptest.NewRecorder()
	receiver.HandleWebhook(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 (wrong secret)", w.Code)
	}

	// Correct secret.
	req = httptest.NewRequest(http.MethodPost, "/aperture/webhook", bytes.NewReader(body))
	req.Header.Set("X-Webhook-Secret", "my-secret-token")
	w = httptest.NewRecorder()
	receiver.HandleWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (correct secret)", w.Code)
	}
}

func TestWebhookNoSecretAllowsAll(t *testing.T) {
	receiver := NewApertureWebhookReceiver(100, nil, "")

	body, _ := json.Marshal(ApertureEvent{
		Type:  "llm_call",
		Agent: "openclaw",
	})

	req := httptest.NewRequest(http.MethodPost, "/aperture/webhook", bytes.NewReader(body))
	w := httptest.NewRecorder()
	receiver.HandleWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (no secret configured)", w.Code)
	}
}
