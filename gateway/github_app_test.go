package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// generateTestKey creates an RSA private key for testing.
func generateTestKey(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	return key, string(pemBytes)
}

func TestParseRSAPrivateKey_PKCS1(t *testing.T) {
	_, pemStr := generateTestKey(t)
	key, err := parseRSAPrivateKey(pemStr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key == nil {
		t.Fatal("expected non-nil key")
	}
}

func TestParseRSAPrivateKey_PKCS8(t *testing.T) {
	key, _ := generateTestKey(t)
	pkcs8Bytes, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal PKCS8: %v", err)
	}
	pemStr := string(pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: pkcs8Bytes,
	}))

	parsed, err := parseRSAPrivateKey(pemStr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed == nil {
		t.Fatal("expected non-nil key")
	}
}

func TestParseRSAPrivateKey_InvalidPEM(t *testing.T) {
	_, err := parseRSAPrivateKey("not a pem")
	if err == nil {
		t.Fatal("expected error for invalid PEM")
	}
	if !strings.Contains(err.Error(), "no PEM block") {
		t.Fatalf("expected 'no PEM block', got: %v", err)
	}
}

func TestBase64URLEncode(t *testing.T) {
	result := base64URLEncode([]byte("hello"))
	if strings.Contains(result, "+") || strings.Contains(result, "/") || strings.Contains(result, "=") {
		t.Fatalf("result contains non-URL-safe characters: %s", result)
	}
	if result != "aGVsbG8" {
		t.Fatalf("unexpected encoding: %s", result)
	}
}

func TestCreateJWT(t *testing.T) {
	key, _ := generateTestKey(t)
	provider := &AppTokenProvider{
		appID:      "12345",
		privateKey: key,
	}

	jwt, err := provider.createJWT()
	if err != nil {
		t.Fatalf("createJWT: %v", err)
	}

	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Fatalf("expected 3 JWT parts, got %d", len(parts))
	}

	// Verify header.
	if parts[0] != base64URLEncode([]byte(`{"alg":"RS256","typ":"JWT"}`)) {
		t.Fatalf("unexpected header: %s", parts[0])
	}
}

func TestTokenCaching(t *testing.T) {
	_, pemStr := generateTestKey(t)
	calls := 0

	// Mock GitHub API.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/app/installations" {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode([]map[string]int64{{"id": 99999}})
			return
		}
		if strings.Contains(r.URL.Path, "/access_tokens") {
			calls++
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]any{
				"token":      fmt.Sprintf("ghs_test_token_%d", calls),
				"expires_at": time.Now().Add(1 * time.Hour).Format(time.RFC3339),
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	key, err := parseRSAPrivateKey(pemStr)
	if err != nil {
		t.Fatal(err)
	}

	provider := &AppTokenProvider{
		appID:      "12345",
		privateKey: key,
		httpClient: server.Client(),
		apiBase:    server.URL,
	}

	// First call should hit the server.
	token1, err := provider.Token()
	if err != nil {
		t.Fatalf("first token: %v", err)
	}
	if !strings.HasPrefix(token1, "ghs_test_token_") {
		t.Fatalf("unexpected token: %s", token1)
	}
	if calls != 1 {
		t.Fatalf("expected 1 server call, got %d", calls)
	}

	// Second call should use cache (>10min remaining).
	token2, err := provider.Token()
	if err != nil {
		t.Fatalf("second token: %v", err)
	}
	if token2 != token1 {
		t.Fatalf("expected cached token, got different: %s vs %s", token1, token2)
	}
	if calls != 1 {
		t.Fatalf("expected cache hit (still 1 call), got %d", calls)
	}

	// Simulate near-expiry.
	provider.expiresAt = time.Now().Add(5 * time.Minute) // <10min triggers refresh

	token3, err := provider.Token()
	if err != nil {
		t.Fatalf("third token: %v", err)
	}
	if token3 == token1 {
		t.Fatal("expected refreshed token, got same")
	}
	if calls != 2 {
		t.Fatalf("expected 2 server calls, got %d", calls)
	}
}

func TestInstallationAutoDetect(t *testing.T) {
	_, pemStr := generateTestKey(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/app/installations" {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode([]map[string]int64{{"id": 112325685}})
			return
		}
		if strings.Contains(r.URL.Path, "/access_tokens") {
			// Verify correct installation ID was used.
			if !strings.Contains(r.URL.Path, "112325685") {
				t.Errorf("expected installation ID 112325685, got path: %s", r.URL.Path)
			}
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]any{
				"token":      "ghs_autodetected",
				"expires_at": time.Now().Add(1 * time.Hour).Format(time.RFC3339),
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	key, _ := parseRSAPrivateKey(pemStr)
	provider := &AppTokenProvider{
		appID:      "2945224",
		installID:  "", // Should auto-detect.
		privateKey: key,
		httpClient: server.Client(),
		apiBase:    server.URL,
	}

	token, err := provider.Token()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if token != "ghs_autodetected" {
		t.Fatalf("expected autodetected token, got: %s", token)
	}
	if provider.installID != "112325685" {
		t.Fatalf("expected cached install ID, got: %s", provider.installID)
	}
}

func TestTokenExchangeError(t *testing.T) {
	_, pemStr := generateTestKey(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/app/installations" {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode([]map[string]int64{{"id": 1}})
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"message":"Bad credentials"}`))
	}))
	defer server.Close()

	key, _ := parseRSAPrivateKey(pemStr)
	provider := &AppTokenProvider{
		appID:      "12345",
		privateKey: key,
		httpClient: server.Client(),
		apiBase:    server.URL,
	}

	_, err := provider.Token()
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("expected 401 in error, got: %v", err)
	}
}

func TestNewAppTokenProvider_MissingEnv(t *testing.T) {
	// Clear env.
	os.Unsetenv("GITHUB_APP_ID")
	os.Unsetenv("GITHUB_APP_PRIVATE_KEY")

	_, err := NewAppTokenProvider()
	if err == nil {
		t.Fatal("expected error when GITHUB_APP_ID not set")
	}

	t.Setenv("GITHUB_APP_ID", "12345")
	_, err = NewAppTokenProvider()
	if err == nil {
		t.Fatal("expected error when GITHUB_APP_PRIVATE_KEY not set")
	}
}

func TestNewAppTokenProvider_FromEnv(t *testing.T) {
	_, pemStr := generateTestKey(t)
	t.Setenv("GITHUB_APP_ID", "12345")
	t.Setenv("GITHUB_APP_PRIVATE_KEY", pemStr)
	t.Setenv("GITHUB_APP_INSTALL_ID", "99999")

	provider, err := NewAppTokenProvider()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider.appID != "12345" {
		t.Fatalf("expected appID 12345, got %s", provider.appID)
	}
	if provider.installID != "99999" {
		t.Fatalf("expected installID 99999, got %s", provider.installID)
	}
}

func TestNewAppTokenProvider_KeyFromFile(t *testing.T) {
	_, pemStr := generateTestKey(t)
	keyFile := t.TempDir() + "/test-key.pem"
	if err := os.WriteFile(keyFile, []byte(pemStr), 0600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("GITHUB_APP_ID", "12345")
	t.Setenv("GITHUB_APP_PRIVATE_KEY", keyFile)

	provider, err := NewAppTokenProvider()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider.privateKey == nil {
		t.Fatal("expected non-nil key from file")
	}
}

func TestNewAppTokenProviderFromCredentials(t *testing.T) {
	_, pemStr := generateTestKey(t)

	provider, err := NewAppTokenProviderFromCredentials("12345", pemStr, "99999")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider.appID != "12345" {
		t.Fatalf("expected appID 12345, got %s", provider.appID)
	}
	if provider.installID != "99999" {
		t.Fatalf("expected installID 99999, got %s", provider.installID)
	}
	if provider.privateKey == nil {
		t.Fatal("expected non-nil key")
	}
}

func TestNewAppTokenProviderFromCredentials_EmptyInstallID(t *testing.T) {
	_, pemStr := generateTestKey(t)

	provider, err := NewAppTokenProviderFromCredentials("12345", pemStr, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider.installID != "" {
		t.Fatalf("expected empty installID, got %s", provider.installID)
	}
}

func TestNewAppTokenProviderFromCredentials_MissingAppID(t *testing.T) {
	_, pemStr := generateTestKey(t)

	_, err := NewAppTokenProviderFromCredentials("", pemStr, "")
	if err == nil {
		t.Fatal("expected error when appID is empty")
	}
	if !strings.Contains(err.Error(), "appID") {
		t.Fatalf("expected appID error, got: %v", err)
	}
}

func TestNewAppTokenProviderFromCredentials_MissingKey(t *testing.T) {
	_, err := NewAppTokenProviderFromCredentials("12345", "", "")
	if err == nil {
		t.Fatal("expected error when privateKeyPEM is empty")
	}
	if !strings.Contains(err.Error(), "privateKeyPEM") {
		t.Fatalf("expected privateKeyPEM error, got: %v", err)
	}
}

func TestNewAppTokenProviderFromCredentials_InvalidKey(t *testing.T) {
	_, err := NewAppTokenProviderFromCredentials("12345", "not-a-pem-key", "")
	if err == nil {
		t.Fatal("expected error for invalid PEM key")
	}
	if !strings.Contains(err.Error(), "parse private key") {
		t.Fatalf("expected parse error, got: %v", err)
	}
}

func TestAppTokenIntegrationWithGitHubTools(t *testing.T) {
	// Verify that AppTokenProvider works correctly as a tokenFunc for GitHubToolHandler.
	_, pemStr := generateTestKey(t)

	// Mock both the GitHub App API and the regular GitHub API.
	appCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// App token exchange endpoints.
		if r.URL.Path == "/app/installations" {
			json.NewEncoder(w).Encode([]map[string]int64{{"id": 1}})
			return
		}
		if strings.Contains(r.URL.Path, "/access_tokens") {
			appCalls++
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]any{
				"token":      "ghs_bot_token",
				"expires_at": time.Now().Add(1 * time.Hour).Format(time.RFC3339),
			})
			return
		}

		// Regular GitHub API: verify the token is the installation token.
		auth := r.Header.Get("Authorization")
		if auth != "Bearer ghs_bot_token" {
			t.Errorf("expected Bearer ghs_bot_token, got: %s", auth)
		}

		// Mock a simple response for list alerts.
		json.NewEncoder(w).Encode([]map[string]any{})
	}))
	defer server.Close()

	key, _ := parseRSAPrivateKey(pemStr)
	appProvider := &AppTokenProvider{
		appID:      "12345",
		privateKey: key,
		httpClient: server.Client(),
		apiBase:    server.URL,
	}

	// Wire AppTokenProvider into GitHubToolHandler, just like main.go does.
	githubTools := NewGitHubToolHandler(func(ctx context.Context) (string, error) {
		return appProvider.Token()
	})
	githubTools.apiBase = server.URL

	args, _ := json.Marshal(map[string]string{
		"owner": "tinyland-inc",
		"repo":  "remote-juggler",
	})

	_, err := githubTools.ListAlerts(context.Background(), args)
	if err != nil {
		t.Fatalf("ListAlerts with App token: %v", err)
	}
	if appCalls != 1 {
		t.Fatalf("expected 1 App token exchange call, got %d", appCalls)
	}
}

func TestFallbackToPAT(t *testing.T) {
	// Verify that when App credentials aren't available, the PAT path works.
	patCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer pat-token-from-setec" {
			t.Errorf("expected Bearer pat-token-from-setec, got: %s", auth)
		}
		json.NewEncoder(w).Encode([]map[string]any{})
	}))
	defer server.Close()

	// Simulate the PAT fallback path from main.go.
	githubTools := NewGitHubToolHandler(func(ctx context.Context) (string, error) {
		patCalled = true
		return "pat-token-from-setec", nil
	})
	githubTools.apiBase = server.URL

	args, _ := json.Marshal(map[string]string{
		"owner": "test",
		"repo":  "test",
	})

	_, err := githubTools.ListAlerts(context.Background(), args)
	if err != nil {
		t.Fatalf("ListAlerts with PAT: %v", err)
	}
	if !patCalled {
		t.Error("expected PAT tokenFunc to be called")
	}
}
