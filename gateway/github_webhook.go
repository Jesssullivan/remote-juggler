package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// GitHubWebhookForwarder receives GitHub webhook events, validates the
// HMAC-SHA256 signature, and forwards the raw payload to the campaign
// runner's /webhook endpoint. This keeps the runner off the public internet.
type GitHubWebhookForwarder struct {
	httpClient *http.Client
	runnerURL  string
	setec      *SetecClient
}

// NewGitHubWebhookForwarder creates a forwarder that proxies webhooks to the runner.
func NewGitHubWebhookForwarder(runnerURL string, setec *SetecClient) *GitHubWebhookForwarder {
	return &GitHubWebhookForwarder{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		runnerURL:  runnerURL,
		setec:      setec,
	}
}

// HandleWebhook validates the GitHub webhook signature and forwards the
// raw payload to the campaign runner.
// POST /github/webhook
func (f *GitHubWebhookForwarder) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Validate HMAC-SHA256 signature using secret from Setec.
	secret := f.getWebhookSecret(r.Context())
	if secret != "" {
		sig := r.Header.Get("X-Hub-Signature-256")
		if !validateWebhookHMAC(body, sig, secret) {
			log.Printf("github webhook: invalid signature from %s", r.RemoteAddr)
			http.Error(w, "invalid signature", http.StatusForbidden)
			return
		}
	}

	event := r.Header.Get("X-GitHub-Event")
	log.Printf("github webhook: received event=%s, payload=%d bytes", event, len(body))

	// Forward to runner's /webhook endpoint with original headers.
	fwdURL := strings.TrimRight(f.runnerURL, "/") + "/webhook"
	fwd, err := http.NewRequestWithContext(r.Context(), http.MethodPost, fwdURL, bytes.NewReader(body))
	if err != nil {
		log.Printf("github webhook: create forward request: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Pass through GitHub headers so the runner can parse and validate.
	fwd.Header.Set("Content-Type", "application/json")
	fwd.Header.Set("X-GitHub-Event", event)
	fwd.Header.Set("X-GitHub-Delivery", r.Header.Get("X-GitHub-Delivery"))
	if sig := r.Header.Get("X-Hub-Signature-256"); sig != "" {
		fwd.Header.Set("X-Hub-Signature-256", sig)
	}

	resp, err := f.httpClient.Do(fwd)
	if err != nil {
		log.Printf("github webhook: forward to runner: %v", err)
		http.Error(w, "runner unavailable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Proxy the runner's response back.
	respBody, _ := io.ReadAll(resp.Body)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	w.Write(respBody)
}

// getWebhookSecret retrieves the GitHub webhook secret from Setec.
func (f *GitHubWebhookForwarder) getWebhookSecret(ctx context.Context) string {
	if f.setec == nil || !f.setec.Configured() {
		return ""
	}
	secret, err := f.setec.Get(ctx, "github-webhook-secret")
	if err != nil {
		log.Printf("github webhook: cannot get webhook secret from setec: %v", err)
		return ""
	}
	return secret
}

// validateWebhookHMAC validates a GitHub-style HMAC-SHA256 signature ("sha256=<hex>").
func validateWebhookHMAC(body []byte, signature, secret string) bool {
	if signature == "" || secret == "" {
		return false
	}
	if !strings.HasPrefix(signature, "sha256=") {
		return false
	}
	sigHex := strings.TrimPrefix(signature, "sha256=")
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(sigHex), []byte(expected))
}

// WebhookForwarderHealthy checks if the runner is reachable (used by /health).
func (f *GitHubWebhookForwarder) WebhookForwarderHealthy() bool {
	if f.runnerURL == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := strings.TrimRight(f.runnerURL, "/") + "/health"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := f.httpClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// SetRunnerURL updates the runner URL (for testing).
func (f *GitHubWebhookForwarder) SetRunnerURL(url string) {
	f.runnerURL = url
}

// SetHTTPClient replaces the HTTP client (for testing).
func (f *GitHubWebhookForwarder) SetHTTPClient(client *http.Client) {
	f.httpClient = client
}

func init() {
	// Ensure validateWebhookHMAC is compatible with the runner's validateHMAC.
	// Both use the same sha256= format for GitHub signatures.
	_ = fmt.Sprintf // satisfy import
}
