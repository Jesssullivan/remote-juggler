package main

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// AppTokenProvider generates and caches GitHub App installation tokens.
// Thread-safe: multiple goroutines can call Token() concurrently.
type AppTokenProvider struct {
	appID      string
	installID  string
	privateKey *rsa.PrivateKey
	httpClient *http.Client
	apiBase    string // defaults to "https://api.github.com"

	mu          sync.Mutex
	cachedToken string
	expiresAt   time.Time
}

// NewAppTokenProvider creates a provider from environment variables.
// Required: GITHUB_APP_ID, GITHUB_APP_PRIVATE_KEY (PEM content or file path).
// Optional: GITHUB_APP_INSTALL_ID (auto-detected if empty).
func NewAppTokenProvider() (*AppTokenProvider, error) {
	appID := os.Getenv("GITHUB_APP_ID")
	if appID == "" {
		return nil, fmt.Errorf("GITHUB_APP_ID not set")
	}

	keyData := os.Getenv("GITHUB_APP_PRIVATE_KEY")
	if keyData == "" {
		return nil, fmt.Errorf("GITHUB_APP_PRIVATE_KEY not set")
	}

	// If it looks like a file path, read it.
	if !strings.HasPrefix(keyData, "-----") {
		if data, err := os.ReadFile(keyData); err == nil {
			keyData = string(data)
		}
	}

	key, err := parseRSAPrivateKey(keyData)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}

	return &AppTokenProvider{
		appID:      appID,
		installID:  os.Getenv("GITHUB_APP_INSTALL_ID"),
		privateKey: key,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		apiBase:    "https://api.github.com",
	}, nil
}

// NewAppTokenProviderFromCredentials creates a provider from explicit credentials
// (typically retrieved from Setec). installID is optional; if empty, it will be
// auto-detected on first Token() call.
func NewAppTokenProviderFromCredentials(appID, privateKeyPEM, installID string) (*AppTokenProvider, error) {
	if appID == "" {
		return nil, fmt.Errorf("appID is required")
	}
	if privateKeyPEM == "" {
		return nil, fmt.Errorf("privateKeyPEM is required")
	}

	key, err := parseRSAPrivateKey(privateKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}

	return &AppTokenProvider{
		appID:      appID,
		installID:  installID,
		privateKey: key,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		apiBase:    "https://api.github.com",
	}, nil
}

// Token returns a valid installation token, refreshing if expired or near expiry.
func (p *AppTokenProvider) Token() (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Return cached token if it has >10 minutes remaining.
	if p.cachedToken != "" && time.Until(p.expiresAt) > 10*time.Minute {
		return p.cachedToken, nil
	}

	token, expiresAt, err := p.generateInstallationToken()
	if err != nil {
		return "", err
	}

	p.cachedToken = token
	p.expiresAt = expiresAt
	return token, nil
}

// generateInstallationToken creates a JWT, optionally auto-detects the
// installation ID, and exchanges the JWT for an installation access token.
func (p *AppTokenProvider) generateInstallationToken() (string, time.Time, error) {
	jwt, err := p.createJWT()
	if err != nil {
		return "", time.Time{}, fmt.Errorf("create JWT: %w", err)
	}

	// Auto-detect installation ID if not configured.
	installID := p.installID
	if installID == "" {
		installID, err = p.detectInstallationID(jwt)
		if err != nil {
			return "", time.Time{}, fmt.Errorf("detect installation: %w", err)
		}
		p.installID = installID
		log.Printf("github-app: auto-detected installation ID %s", installID)
	}

	// Exchange JWT for installation token.
	url := fmt.Sprintf("%s/app/installations/%s/access_tokens", p.apiBase, installID)
	req, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		return "", time.Time{}, err
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("request token: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		return "", time.Time{}, fmt.Errorf("token exchange returned %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", time.Time{}, fmt.Errorf("parse token response: %w", err)
	}
	if result.Token == "" {
		return "", time.Time{}, fmt.Errorf("empty token in response")
	}

	log.Printf("github-app: obtained installation token (expires %s)", result.ExpiresAt.Format(time.RFC3339))
	return result.Token, result.ExpiresAt, nil
}

// createJWT builds an RS256-signed JWT for GitHub App authentication.
func (p *AppTokenProvider) createJWT() (string, error) {
	now := time.Now()
	header := base64URLEncode([]byte(`{"alg":"RS256","typ":"JWT"}`))

	payload := fmt.Sprintf(`{"iss":"%s","iat":%d,"exp":%d}`,
		p.appID,
		now.Add(-60*time.Second).Unix(), // 60s clock skew allowance
		now.Add(10*time.Minute).Unix(),  // 10 minute expiry
	)
	payloadB64 := base64URLEncode([]byte(payload))

	signingInput := header + "." + payloadB64

	hash := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(nil, p.privateKey, crypto.SHA256, hash[:])
	if err != nil {
		return "", fmt.Errorf("sign JWT: %w", err)
	}

	return signingInput + "." + base64URLEncode(sig), nil
}

// detectInstallationID lists installations and returns the first one's ID.
func (p *AppTokenProvider) detectInstallationID(jwt string) (string, error) {
	url := fmt.Sprintf("%s/app/installations", p.apiBase)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("list installations returned %d: %s", resp.StatusCode, string(body))
	}

	var installations []struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(body, &installations); err != nil {
		return "", fmt.Errorf("parse installations: %w", err)
	}
	if len(installations) == 0 {
		return "", fmt.Errorf("no installations found for app %s", p.appID)
	}

	return fmt.Sprintf("%d", installations[0].ID), nil
}

// parseRSAPrivateKey parses a PEM-encoded RSA private key (PKCS1 or PKCS8).
func parseRSAPrivateKey(pemData string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemData))
	if block == nil {
		return nil, fmt.Errorf("no PEM block found")
	}

	// Try PKCS1 first (RSA PRIVATE KEY).
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}

	// Try PKCS8 (PRIVATE KEY).
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse key (tried PKCS1 and PKCS8): %w", err)
	}

	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("key is not RSA (got %T)", parsed)
	}
	return key, nil
}

// base64URLEncode encodes data using base64url (no padding).
func base64URLEncode(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}
