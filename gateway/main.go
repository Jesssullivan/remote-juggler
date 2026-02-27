package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"tailscale.com/tsnet"
)

func main() {
	configPath := flag.String("config", "", "path to gateway config JSON")
	chapelBin := flag.String("chapel-bin", "", "path to remote-juggler binary (overrides config)")
	listen := flag.String("listen", "", "listen address (overrides config); use 'local' to skip tsnet")
	flag.Parse()

	cfg, err := LoadConfig(*configPath)
	if err != nil && *configPath != "" {
		log.Fatalf("load config: %v", err)
	}

	if *chapelBin != "" {
		cfg.ChapelBinary = *chapelBin
	}
	if *listen != "" {
		cfg.Listen = *listen
	}

	// Start MCP subprocess proxy.
	proxy := NewMCPProxy(cfg.ChapelBinary)
	if err := proxy.Start(); err != nil {
		log.Fatalf("start mcp proxy: %v", err)
	}
	defer proxy.Stop()

	// Initialize Setec client.
	// When running on tsnet, we'll replace the HTTP client with a tailnet-routed one.
	var setecHTTPClient *http.Client
	var tsnetServer *tsnet.Server

	// Determine if we should use tsnet or plain local listener.
	useTsnet := cfg.Listen != "local" && os.Getenv("RJ_GATEWAY_LOCAL") == ""

	if useTsnet {
		tsnetServer = &tsnet.Server{
			Hostname: cfg.Tailscale.Hostname,
		}
		if cfg.Tailscale.StateDir != "" {
			tsnetServer.Dir = cfg.Tailscale.StateDir
		}
		if err := tsnetServer.Start(); err != nil {
			log.Fatalf("tsnet start: %v", err)
		}
		defer tsnetServer.Close()

		// Use tsnet's HTTP client so Setec requests route through the tailnet.
		setecHTTPClient = tsnetServer.HTTPClient()
		log.Printf("joined tailnet as %s", cfg.Tailscale.Hostname)
	}

	setec := NewSetecClient(cfg.SetecURL, cfg.SetecPrefix, setecHTTPClient)
	audit := NewAuditLog()
	resolver := NewResolver(proxy, setec, cfg.Precedence)

	// Initialize MCP-layer metering.
	meterStore := NewMeterStore()
	if setec.Configured() {
		meterStore.SetFlushCallback(flushToSetec(setec))
	}

	// Wire gateway tool handlers into the proxy for MCP tool interception.
	aperture := NewApertureClient(cfg.ApertureURL)
	aperture.SetMeterStore(meterStore)
	// Try GitHub App installation tokens (bot attribution) from Setec credentials.
	// Falls back to PAT if App credentials are not available.
	var githubTokenFunc func(ctx context.Context) (string, error)
	if setec.Configured() {
		appID, _ := setec.Get(context.Background(), "github-app-id")
		appKey, _ := setec.Get(context.Background(), "github-app-private-key")
		if appID != "" && appKey != "" {
			installID, _ := setec.Get(context.Background(), "github-app-install-id")
			appProvider, err := NewAppTokenProviderFromCredentials(appID, appKey, installID)
			if err == nil {
				log.Printf("github: using App installation tokens (bot attribution)")
				githubTokenFunc = func(ctx context.Context) (string, error) {
					return appProvider.Token()
				}
			} else {
				log.Printf("github: App token setup failed: %v, falling back to PAT", err)
			}
		}
	}
	if githubTokenFunc == nil {
		log.Printf("github: using PAT from Setec")
		githubTokenFunc = func(ctx context.Context) (string, error) {
			return setec.Get(ctx, "github-token")
		}
	}
	githubTools := NewGitHubToolHandler(githubTokenFunc)
	proxy.resolver = resolver
	proxy.setec = setec
	proxy.audit = audit
	proxy.aperture = aperture
	proxy.meterStore = meterStore
	proxy.github = githubTools
	proxy.campaigns = NewCampaignClient(cfg.CampaignRunnerURL)

	// Start background polling for configured secrets.
	if setec.Configured() && len(cfg.SetecSecrets) > 0 {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		setec.StartPolling(ctx, cfg.SetecSecrets)
		log.Printf("polling %d secrets from setec at %s", len(cfg.SetecSecrets), cfg.SetecURL)
	}

	// Start hourly metering flush to Setec.
	stopFlush := meterStore.StartFlushLoop(context.Background(), 1*time.Hour)
	defer stopFlush()

	// Initialize Aperture webhook receiver for real-time LLM metrics.
	webhookReceiver := NewApertureWebhookReceiver(1000, meterStore, cfg.WebhookSecret)

	// Start Aperture S3 export ingestion if configured.
	s3Ingester := NewApertureS3Ingester(cfg.ApertureS3, meterStore, setecHTTPClient)
	if s3Ingester.Configured() {
		stopS3 := s3Ingester.Start(context.Background())
		defer stopS3()
	}

	// Start Aperture SSE event ingestion for real-time LLM metrics.
	// This connects to Aperture's /api/events SSE stream and feeds
	// metric events into MeterStore (webhooks are configured but not
	// currently fired by Aperture).
	// Use tsnet transport for MagicDNS resolution but with no timeout
	// (SSE is a long-lived connection that must not time out).
	var sseClient *http.Client
	if setecHTTPClient != nil {
		sseClient = &http.Client{
			Transport: setecHTTPClient.Transport,
			Timeout:   0,
		}
	}
	sseIngester := NewApertureSSEIngester(cfg.ApertureURL, meterStore, sseClient)
	if sseIngester.Configured() {
		stopSSE := sseIngester.Start(context.Background())
		defer stopSSE()
	}

	// Start audit S3 exporter if the S3 bucket is configured.
	// Reuses the same S3 bucket as Aperture but writes to a separate prefix.
	auditInterval := 5 * time.Minute
	if cfg.AuditS3Interval != "" {
		if d, err := time.ParseDuration(cfg.AuditS3Interval); err == nil {
			auditInterval = d
		}
	}
	auditExporter := NewAuditS3Exporter(cfg.ApertureS3, cfg.AuditS3Prefix, auditInterval, audit, nil)
	if auditExporter.Configured() {
		stopAudit := auditExporter.Start(context.Background())
		defer stopAudit()
	}

	// Build handler chain.
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", proxy.HandleRPC)
	mux.HandleFunc("/mcp/sse", proxy.HandleSSE)
	mux.HandleFunc("/health", handleHealth)
	mux.HandleFunc("/resolve", handleResolve(resolver, audit))
	mux.HandleFunc("/audit", handleAuditQuery(audit))
	mux.HandleFunc("/setec/list", handleSetecList(setec))
	mux.HandleFunc("/setec/get", handleSetecGet(setec, audit))
	mux.HandleFunc("/aperture/webhook", webhookReceiver.HandleWebhook)
	mux.HandleFunc("/portal", handlePortal)
	mux.HandleFunc("/portal/api", handlePortalAPI(audit, meterStore))

	// Wrap with identity extraction middleware.
	handler := IdentityMiddleware(mux)

	// Get listener.
	var ln net.Listener
	if useTsnet && tsnetServer != nil {
		// Listen on the tailnet with TLS.
		ln, err = tsnetServer.ListenTLS("tcp", cfg.Listen)
		if err != nil {
			log.Fatalf("tsnet listen: %v", err)
		}
		log.Printf("rj-gateway listening on tailnet %s%s", cfg.Tailscale.Hostname, cfg.Listen)
	} else {
		// Local mode for development/testing.
		ln, err = net.Listen("tcp", cfg.Listen)
		if err != nil {
			log.Fatalf("listen: %v", err)
		}
		log.Printf("rj-gateway listening locally on %s", cfg.Listen)
	}

	server := &http.Server{
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Optional in-cluster HTTP listener (no TLS) for pod-to-pod communication.
	var inClusterServer *http.Server
	if cfg.InClusterListen != "" {
		icLn, icErr := net.Listen("tcp", cfg.InClusterListen)
		if icErr != nil {
			log.Fatalf("in-cluster listen: %v", icErr)
		}
		inClusterServer = &http.Server{
			Handler:      handler,
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 60 * time.Second,
			IdleTimeout:  120 * time.Second,
		}
		go func() {
			if err := inClusterServer.Serve(icLn); err != http.ErrServerClosed {
				log.Fatalf("in-cluster server error: %v", err)
			}
		}()
		log.Printf("rj-gateway in-cluster listener on %s (HTTP, no TLS)", cfg.InClusterListen)
	}

	// Graceful shutdown.
	done := make(chan os.Signal, 1)
	signal.Notify(done, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		if err := server.Serve(ln); err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	<-done
	log.Println("shutting down...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server.Shutdown(ctx)
	if inClusterServer != nil {
		inClusterServer.Shutdown(ctx)
	}
	setec.StopPolling()
}

// handleHealth returns gateway status including Setec and tsnet connectivity.
func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"service": "rj-gateway",
		"version": "2.3.0",
	})
}

// handleResolve handles the composite secret resolution endpoint.
func handleResolve(resolver *Resolver, audit *AuditLog) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
			return
		}

		var req struct {
			Query   string   `json:"query"`
			Sources []string `json:"sources"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, "parse body: "+err.Error(), http.StatusBadRequest)
			return
		}

		if req.Query == "" {
			http.Error(w, `missing "query" field`, http.StatusBadRequest)
			return
		}

		caller, _ := IdentityFromContext(r.Context())
		result := resolver.Resolve(r.Context(), req.Query, req.Sources)

		// Audit log the access.
		audit.Log(AuditEntry{
			Caller:  caller,
			Action:  "resolve_composite",
			Query:   req.Query,
			Source:  result.Source,
			Allowed: result.Error == "",
			Reason:  result.Error,
		})

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	}
}

// handleAuditQuery returns recent audit log entries (for the Aperture dashboard).
func handleAuditQuery(audit *AuditLog) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		entries := audit.Recent(50)
		json.NewEncoder(w).Encode(map[string]any{
			"entries": entries,
			"count":   len(entries),
		})
	}
}

// handleSetecList lists available secrets in Setec.
func handleSetecList(setec *SetecClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		names, err := setec.List(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"secrets": names})
	}
}

// handleSetecGet retrieves a single secret from Setec.
func handleSetecGet(setec *SetecClient, audit *AuditLog) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "parse: "+err.Error(), http.StatusBadRequest)
			return
		}

		caller, _ := IdentityFromContext(r.Context())

		val, err := setec.Get(r.Context(), req.Name)
		if err != nil {
			audit.Log(AuditEntry{
				Caller:  caller,
				Action:  "setec_get",
				Query:   req.Name,
				Allowed: false,
				Reason:  err.Error(),
			})
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}

		audit.Log(AuditEntry{
			Caller:  caller,
			Action:  "setec_get",
			Query:   req.Name,
			Source:  "setec",
			Allowed: true,
		})

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"value": val})
	}
}

// handleResolveAsMCPTool wraps the resolver as a JSON-RPC MCP tool response.
func handleResolveAsMCPTool(resolver *Resolver, params json.RawMessage) (json.RawMessage, error) {
	var args struct {
		Query   string   `json:"query"`
		Sources []string `json:"sources"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("parse args: %w", err)
	}

	result := resolver.Resolve(context.Background(), args.Query, args.Sources)

	content := []map[string]any{{
		"type": "text",
		"text": mustMarshal(result),
	}}
	resp := map[string]any{"content": content}
	return json.Marshal(resp)
}

func mustMarshal(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
