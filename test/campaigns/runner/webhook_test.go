package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newWebhookTestAPI() *APIServer {
	registry := map[string]*Campaign{
		"push-gateway": {
			ID:    "push-gateway",
			Name:  "Gateway Push Check",
			Agent: "gateway-direct",
			Trigger: CampaignTrigger{
				Event:       "push",
				PathFilters: []string{"gateway/*.go"},
			},
			Targets: []Target{
				{Forge: "github", Org: "tinyland-inc", Repo: "remote-juggler", Branch: "main"},
			},
			Tools:      []string{"juggler_setec_list"},
			Guardrails: Guardrails{MaxDuration: "5m"},
		},
		"pr-all": {
			ID:    "pr-all",
			Name:  "PR All Files",
			Agent: "openclaw",
			Trigger: CampaignTrigger{
				Event: "pull_request",
			},
			Targets: []Target{
				{Forge: "github", Org: "tinyland-inc", Repo: "remote-juggler"},
			},
			Tools:      []string{"juggler_audit_log"},
			Guardrails: Guardrails{MaxDuration: "5m"},
		},
		"push-wildcard": {
			ID:    "push-wildcard",
			Name:  "Push Any Repo",
			Agent: "gateway-direct",
			Trigger: CampaignTrigger{
				Event: "push",
			},
			Targets: []Target{
				{Forge: "github", Org: "tinyland-inc", Repo: "*"},
			},
			Tools:      []string{"juggler_setec_list"},
			Guardrails: Guardrails{MaxDuration: "5m"},
		},
		"cron-only": {
			ID:    "cron-only",
			Name:  "Cron Only",
			Agent: "gateway-direct",
			Trigger: CampaignTrigger{
				Schedule: "0 4 * * 1",
			},
			Targets: []Target{
				{Forge: "github", Org: "tinyland-inc", Repo: "remote-juggler"},
			},
			Tools:      []string{"juggler_setec_list"},
			Guardrails: Guardrails{MaxDuration: "5m"},
		},
	}

	scheduler := NewScheduler(registry, nil, nil)
	return NewAPIServer(scheduler, registry)
}

func TestWebhookPushMatchesPathFilter(t *testing.T) {
	api := newWebhookTestAPI()

	payload := WebhookPayload{
		Event:        "push",
		Forge:        "github",
		Ref:          "refs/heads/main",
		Repo:         "tinyland-inc/remote-juggler",
		ChangedFiles: []string{"gateway/main.go", "README.md"},
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	w := httptest.NewRecorder()
	api.handleWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)

	triggered := resp["triggered"].([]any)
	names := make(map[string]bool)
	for _, v := range triggered {
		names[v.(string)] = true
	}
	if !names["push-gateway"] {
		t.Error("expected push-gateway to be triggered (path filter matches gateway/*.go)")
	}
	if !names["push-wildcard"] {
		t.Error("expected push-wildcard to be triggered (repo wildcard)")
	}
}

func TestWebhookPushNoPathMatch(t *testing.T) {
	api := newWebhookTestAPI()

	payload := WebhookPayload{
		Event:        "push",
		Forge:        "github",
		Ref:          "refs/heads/main",
		Repo:         "tinyland-inc/remote-juggler",
		ChangedFiles: []string{"docs/readme.md"},
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	w := httptest.NewRecorder()
	api.handleWebhook(w, req)

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)

	triggered := resp["triggered"].([]any)
	for _, v := range triggered {
		if v.(string) == "push-gateway" {
			t.Error("push-gateway should NOT be triggered (no gateway/*.go files changed)")
		}
	}
}

func TestWebhookPRTrigger(t *testing.T) {
	api := newWebhookTestAPI()

	payload := WebhookPayload{
		Event: "pull_request",
		Forge: "github",
		Repo:  "tinyland-inc/remote-juggler",
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	w := httptest.NewRecorder()
	api.handleWebhook(w, req)

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)

	triggered := resp["triggered"].([]any)
	found := false
	for _, v := range triggered {
		if v.(string) == "pr-all" {
			found = true
		}
	}
	if !found {
		t.Error("expected pr-all to be triggered")
	}
}

func TestWebhookCronNotTriggered(t *testing.T) {
	api := newWebhookTestAPI()

	payload := WebhookPayload{
		Event: "push",
		Forge: "github",
		Repo:  "tinyland-inc/remote-juggler",
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	w := httptest.NewRecorder()
	api.handleWebhook(w, req)

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)

	triggered := resp["triggered"].([]any)
	for _, v := range triggered {
		if v.(string) == "cron-only" {
			t.Error("cron-only should NOT be triggered by webhooks")
		}
	}
}

func TestWebhookMethodNotAllowed(t *testing.T) {
	api := newWebhookTestAPI()

	req := httptest.NewRequest(http.MethodGet, "/webhook", nil)
	w := httptest.NewRecorder()
	api.handleWebhook(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", w.Code)
	}
}

func TestWebhookMissingFields(t *testing.T) {
	api := newWebhookTestAPI()

	body, _ := json.Marshal(map[string]string{"event": "push"})
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	w := httptest.NewRecorder()
	api.handleWebhook(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for missing repo", w.Code)
	}
}

func TestWebhookHMACValidation(t *testing.T) {
	api := newWebhookTestAPI()
	api.WebhookSecret = "test-secret-123"

	payload := WebhookPayload{
		Event: "push",
		Forge: "github",
		Repo:  "tinyland-inc/remote-juggler",
	}
	body, _ := json.Marshal(payload)

	// Valid signature.
	mac := hmac.New(sha256.New, []byte("test-secret-123"))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", sig)
	w := httptest.NewRecorder()
	api.handleWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("valid HMAC: status = %d, want 200", w.Code)
	}
}

func TestWebhookHMACInvalid(t *testing.T) {
	api := newWebhookTestAPI()
	api.WebhookSecret = "test-secret-123"

	body, _ := json.Marshal(WebhookPayload{Event: "push", Repo: "org/repo"})

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", "sha256=badhex")
	w := httptest.NewRecorder()
	api.handleWebhook(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("invalid HMAC: status = %d, want 403", w.Code)
	}
}

func TestWebhookHMACMissing(t *testing.T) {
	api := newWebhookTestAPI()
	api.WebhookSecret = "test-secret-123"

	body, _ := json.Marshal(WebhookPayload{Event: "push", Repo: "org/repo"})

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	// No signature header.
	w := httptest.NewRecorder()
	api.handleWebhook(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("missing HMAC: status = %d, want 403", w.Code)
	}
}

func TestWebhookGitLabToken(t *testing.T) {
	api := newWebhookTestAPI()
	api.WebhookSecret = "gl-token-abc"

	body, _ := json.Marshal(WebhookPayload{
		Event: "push",
		Forge: "gitlab",
		Repo:  "tinyland-inc/remote-juggler",
	})

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("X-Gitlab-Token", "gl-token-abc")
	w := httptest.NewRecorder()
	api.handleWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("GitLab token: status = %d, want 200", w.Code)
	}
}

func TestPathFiltersMatch(t *testing.T) {
	tests := []struct {
		name    string
		filters []string
		files   []string
		want    bool
	}{
		{
			name:    "exact glob match",
			filters: []string{"gateway/*.go"},
			files:   []string{"gateway/main.go"},
			want:    true,
		},
		{
			name:    "no match",
			filters: []string{"gateway/*.go"},
			files:   []string{"src/main.go"},
			want:    false,
		},
		{
			name:    "multiple filters, one matches",
			filters: []string{"docs/*", "gateway/*.go"},
			files:   []string{"gateway/config.go"},
			want:    true,
		},
		{
			name:    "wildcard extension",
			filters: []string{"*.py"},
			files:   []string{"test.py"},
			want:    true,
		},
		{
			name:    "empty files",
			filters: []string{"*.go"},
			files:   []string{},
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pathFiltersMatch(tt.filters, tt.files)
			if got != tt.want {
				t.Errorf("pathFiltersMatch(%v, %v) = %v, want %v", tt.filters, tt.files, got, tt.want)
			}
		})
	}
}

func TestValidateHMAC(t *testing.T) {
	secret := "mysecret"
	body := []byte("hello world")

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	validSig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	if !validateHMAC(body, validSig, secret) {
		t.Error("valid HMAC should return true")
	}
	if validateHMAC(body, "sha256=invalid", secret) {
		t.Error("invalid HMAC should return false")
	}
	if validateHMAC(body, "", secret) {
		t.Error("empty signature should return false")
	}

	// GitLab raw token.
	if !validateHMAC(body, secret, secret) {
		t.Error("GitLab token match should return true")
	}
	if validateHMAC(body, "wrong", secret) {
		t.Error("GitLab token mismatch should return false")
	}
}
