package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandlePortalAPI(t *testing.T) {
	tests := []struct {
		name        string
		audit       *AuditLog
		meterStore  *MeterStore
		expectUsage bool
	}{
		{
			name:        "with meter store",
			audit:       &AuditLog{},
			meterStore:  &MeterStore{},
			expectUsage: true,
		},
		{
			name:        "without meter store",
			audit:       &AuditLog{},
			meterStore:  nil,
			expectUsage: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create the handler
			handler := handlePortalAPI(tt.audit, tt.meterStore)

			// Create test request
			req := httptest.NewRequest("GET", "/portal/api", nil)
			w := httptest.NewRecorder()

			// Execute the handler
			handler(w, req)

			// Verify response
			if w.Code != http.StatusOK {
				t.Errorf("Expected status 200, got %d", w.Code)
			}

			if contentType := w.Header().Get("Content-Type"); contentType != "application/json" {
				t.Errorf("Expected Content-Type application/json, got %s", contentType)
			}

			// Parse response JSON
			var response map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatalf("Failed to parse JSON response: %v", err)
			}

			// Verify required fields
			if _, ok := response["agents"]; !ok {
				t.Error("Response missing 'agents' field")
			}

			if _, ok := response["audit"]; !ok {
				t.Error("Response missing 'audit' field")
			}

			// Verify agents structure
			agents, ok := response["agents"].([]any)
			if !ok {
				t.Error("'agents' field is not an array")
			} else if len(agents) != 3 {
				t.Errorf("Expected 3 agents, got %d", len(agents))
			}

			// Verify usage field based on meter store presence
			usage, hasUsage := response["usage"]
			if tt.expectUsage {
				if !hasUsage {
					t.Error("Expected 'usage' field when meter store provided")
				} else if usage == nil {
					t.Error("'usage' field should not be nil when meter store provided")
				}
			} else {
				if hasUsage && usage != nil {
					t.Error("'usage' field should be nil when meter store is nil")
				}
			}
		})
	}
}

// TestHandlePortalAPIAgentStructure tests the agent data structure
func TestHandlePortalAPIAgentStructure(t *testing.T) {
	audit := &AuditLog{}
	handler := handlePortalAPI(audit, nil)

	req := httptest.NewRequest("GET", "/portal/api", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	var response map[string]any
	json.Unmarshal(w.Body.Bytes(), &response)

	agents := response["agents"].([]any)
	expectedAgents := []string{"IronClaw", "TinyClaw", "HexStrike-AI"}

	for i, expectedName := range expectedAgents {
		agent := agents[i].(map[string]any)
		if name := agent["name"].(string); name != expectedName {
			t.Errorf("Expected agent %d to be %s, got %s", i, expectedName, name)
		}

		// Verify required fields exist
		requiredFields := []string{"type", "url", "role", "adapter"}
		for _, field := range requiredFields {
			if _, ok := agent[field]; !ok {
				t.Errorf("Agent %s missing required field %s", expectedName, field)
			}
		}
	}
}
