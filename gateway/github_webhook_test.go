package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidateWebhookHMAC(t *testing.T) {
	secret := "test-secret-123"
	body := []byte(`{"action":"created","comment":{"body":"@rj-agent-bot hello"}}`)

	// Compute valid signature.
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	validSig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	tests := []struct {
		name      string
		body      []byte
		signature string
		secret    string
		want      bool
	}{
		{"valid signature", body, validSig, secret, true},
		{"wrong signature", body, "sha256=deadbeef", secret, false},
		{"empty signature", body, "", secret, false},
		{"empty secret", body, validSig, "", false},
		{"wrong body", []byte("tampered"), validSig, secret, false},
		{"no sha256 prefix", body, "deadbeef", secret, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := validateWebhookHMAC(tt.body, tt.signature, tt.secret)
			if got != tt.want {
				t.Errorf("validateWebhookHMAC() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGitHubWebhookForwarder_ValidSignature(t *testing.T) {
	secret := "webhook-secret"
	body := `{"action":"created"}`

	// Mock runner backend.
	runner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/webhook" {
			t.Errorf("expected path /webhook, got %s", r.URL.Path)
		}
		if r.Header.Get("X-GitHub-Event") != "issue_comment" {
			t.Errorf("missing X-GitHub-Event header")
		}
		b, _ := io.ReadAll(r.Body)
		if string(b) != body {
			t.Errorf("body mismatch: got %q", string(b))
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"triggered":["conv-1"]}`))
	}))
	defer runner.Close()

	fwd := NewGitHubWebhookForwarder(runner.URL, nil)

	// Compute HMAC.
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	// Since setec is nil, HMAC validation is skipped (no secret configured).
	req := httptest.NewRequest(http.MethodPost, "/github/webhook", strings.NewReader(body))
	req.Header.Set("X-GitHub-Event", "issue_comment")
	req.Header.Set("X-Hub-Signature-256", sig)
	req.Header.Set("X-GitHub-Delivery", "abc-123")
	w := httptest.NewRecorder()

	fwd.HandleWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestGitHubWebhookForwarder_MethodNotAllowed(t *testing.T) {
	fwd := NewGitHubWebhookForwarder("http://localhost:9999", nil)

	req := httptest.NewRequest(http.MethodGet, "/github/webhook", nil)
	w := httptest.NewRecorder()

	fwd.HandleWebhook(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestGitHubWebhookForwarder_RunnerUnavailable(t *testing.T) {
	fwd := NewGitHubWebhookForwarder("http://localhost:1", nil) // unreachable

	req := httptest.NewRequest(http.MethodPost, "/github/webhook", strings.NewReader(`{}`))
	req.Header.Set("X-GitHub-Event", "push")
	w := httptest.NewRecorder()

	fwd.HandleWebhook(w, req)

	if w.Code != http.StatusBadGateway {
		t.Errorf("expected 502, got %d", w.Code)
	}
}
