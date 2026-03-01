package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// CallerIdentity represents the Tailscale identity of an incoming request.
type CallerIdentity struct {
	// Node is the Tailscale node name (e.g. "macbook-pro").
	Node string `json:"node"`
	// User is the Tailscale user (e.g. "user@example.com").
	User string `json:"user"`
	// Login is the login name portion (e.g. "user").
	Login string `json:"login"`
	// Capabilities are Tailscale app capabilities from grants.
	Capabilities []string `json:"capabilities,omitempty"`
	// TailnetIP is the Tailscale IP of the caller.
	TailnetIP string `json:"tailnet_ip,omitempty"`
}

type contextKey string

const identityKey contextKey = "caller-identity"

// IdentityFromContext extracts the caller identity from the request context.
func IdentityFromContext(ctx context.Context) (CallerIdentity, bool) {
	id, ok := ctx.Value(identityKey).(CallerIdentity)
	return id, ok
}

// IdentityMiddleware extracts Tailscale identity headers set by tsnet/Tailscale Serve
// and injects them into the request context.
func IdentityMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := CallerIdentity{
			Node:      r.Header.Get("Tailscale-User-Name"),
			User:      r.Header.Get("Tailscale-User-Login"),
			Login:     r.Header.Get("Tailscale-User-Login"),
			TailnetIP: r.Header.Get("Tailscale-User-Addr"),
		}

		// Parse app capabilities from grants header.
		if caps := r.Header.Get("Tailscale-App-Capabilities"); caps != "" {
			id.Capabilities = parseCapabilities(caps)
		}

		// Fallback: use X-Agent-Identity header from in-cluster agents.
		if id.User == "" {
			if agentID := r.Header.Get("X-Agent-Identity"); agentID != "" {
				id.Node = agentID
				id.Login = agentID
				id.User = agentID + "@agent.tinyland.dev"
			}
		}

		// Final fallback: extract from remote addr.
		if id.User == "" && r.RemoteAddr != "" {
			id.TailnetIP = strings.Split(r.RemoteAddr, ":")[0]
			id.Node = id.TailnetIP // best effort
		}

		ctx := context.WithValue(r.Context(), identityKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// AuditEntry records a credential access event.
type AuditEntry struct {
	Timestamp  time.Time      `json:"timestamp"`
	Caller     CallerIdentity `json:"caller"`
	Action     string         `json:"action"`
	Query      string         `json:"query"`
	Source     string         `json:"source,omitempty"`
	Allowed    bool           `json:"allowed"`
	Reason     string         `json:"reason,omitempty"`
	CampaignID string         `json:"campaign_id,omitempty"`
}

// AuditLog provides structured logging for credential access events.
// It keeps a ring buffer of recent entries for the /audit endpoint.
type AuditLog struct {
	mu      sync.Mutex
	entries []AuditEntry
	maxSize int
}

// NewAuditLog creates a new audit logger with a 1000-entry ring buffer.
func NewAuditLog() *AuditLog {
	return &AuditLog{
		entries: make([]AuditEntry, 0, 1000),
		maxSize: 1000,
	}
}

// Log records an audit entry as structured JSON to stdout and stores it.
func (a *AuditLog) Log(entry AuditEntry) {
	entry.Timestamp = time.Now().UTC()
	data, err := json.Marshal(entry)
	if err != nil {
		log.Printf("audit: marshal error: %v", err)
		return
	}
	log.Printf("AUDIT: %s", string(data))

	a.mu.Lock()
	if len(a.entries) >= a.maxSize {
		// Drop oldest 10%.
		drop := a.maxSize / 10
		a.entries = a.entries[drop:]
	}
	a.entries = append(a.entries, entry)
	a.mu.Unlock()
}

// Drain returns all entries added since the given index. Used by the S3
// exporter for incremental export without removing entries from the buffer.
func (a *AuditLog) Drain(since int) []AuditEntry {
	a.mu.Lock()
	defer a.mu.Unlock()

	total := len(a.entries)
	if since >= total {
		return nil
	}
	result := make([]AuditEntry, total-since)
	copy(result, a.entries[since:])
	return result
}

// Recent returns the last n audit entries, newest first.
func (a *AuditLog) Recent(n int) []AuditEntry {
	a.mu.Lock()
	defer a.mu.Unlock()

	if n <= 0 {
		return nil
	}
	const maxReturn = 10000
	if n > maxReturn {
		n = maxReturn
	}
	total := len(a.entries)
	if n > total {
		n = total
	}
	if n == 0 {
		return nil
	}
	// Return in reverse order (newest first).
	result := make([]AuditEntry, n) //nolint:gosec // n is bounded by maxReturn and total above
	for i := 0; i < n; i++ {
		result[i] = a.entries[total-1-i]
	}
	return result
}

// parseCapabilities splits the capabilities header value.
func parseCapabilities(header string) []string {
	var caps []string
	for _, c := range strings.Split(header, ",") {
		c = strings.TrimSpace(c)
		if c != "" {
			caps = append(caps, c)
		}
	}
	return caps
}

// HasCapability checks if the caller has a specific app capability.
func (id CallerIdentity) HasCapability(cap string) bool {
	for _, c := range id.Capabilities {
		if c == cap {
			return true
		}
	}
	return false
}

// GrantsMiddleware enforces Tailscale app capabilities for secret access.
// If requireCap is non-empty, requests without that capability are rejected.
func GrantsMiddleware(requireCap string, next http.Handler) http.Handler {
	if requireCap == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, _ := IdentityFromContext(r.Context())
		if !id.HasCapability(requireCap) {
			http.Error(w, "forbidden: missing capability "+requireCap, http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r.WithContext(r.Context()))
	})
}
