// Campaign runner: orchestration controller for RemoteJuggler agent test campaigns.
//
// Runs as a sidecar in the IronClaw pod. Reads campaign definitions from
// a mounted ConfigMap, evaluates triggers (cron, event, manual), and dispatches
// work to agents (IronClaw, PicoClaw, HexStrike-AI) via adapter sidecars or
// rj-gateway MCP tool calls. Results are collected and stored in Setec.
//
// Usage:
//
//	campaign-runner [flags]
//	  --campaigns-dir  path to campaign definitions (default /etc/campaigns)
//	  --gateway-url    rj-gateway MCP endpoint (default https://rj-gateway:443)
//	  --once           run all due campaigns once and exit (for testing)
//	  --campaign       run a specific campaign by ID and exit
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

func main() {
	campaignsDir := flag.String("campaigns-dir", envOrDefault("CAMPAIGNS_DIR", "/etc/campaigns"), "path to campaign definitions")
	gatewayURL := flag.String("gateway-url", envOrDefault("RJ_GATEWAY_URL", "https://rj-gateway:443"), "rj-gateway MCP endpoint")
	ironclawURL := flag.String("ironclaw-url", envOrDefault("IRONCLAW_URL", ""), "ironclaw adapter URL (same pod or K8s Service)")
	picoclawURL := flag.String("picoclaw-url", envOrDefault("PICOCLAW_URL", ""), "picoclaw adapter URL (K8s Service)")
	hexstrikeAIURL := flag.String("hexstrike-ai-url", envOrDefault("HEXSTRIKE_AI_URL", ""), "hexstrike-ai adapter URL (K8s Service)")
	// Backward compat: --hexstrike-url is an alias for --hexstrike-ai-url.
	hexstrikeURLAlias := flag.String("hexstrike-url", envOrDefault("HEXSTRIKE_URL", ""), "deprecated: use --hexstrike-ai-url")
	once := flag.Bool("once", false, "run all due campaigns once and exit")
	campaignID := flag.String("campaign", "", "run a specific campaign by ID and exit")
	interval := flag.Duration("interval", 60*time.Second, "scheduler check interval")
	apiPort := flag.Int("api-port", intEnvOrDefault("CAMPAIGN_RUNNER_API_PORT", 8081), "HTTP API server port (0 to disable)")
	flag.Parse()

	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("campaign-runner: ")

	// Load campaign definitions from ConfigMap directory.
	registry := loadCampaigns(*campaignsDir)

	// Resolve hexstrike-ai URL: prefer new flag, fall back to legacy alias.
	hsURL := *hexstrikeAIURL
	if hsURL == "" {
		hsURL = *hexstrikeURLAlias
	}

	dispatcher := NewDispatcher(*gatewayURL, *ironclawURL, *picoclawURL, hsURL)
	collector := NewCollector(*gatewayURL)
	scheduler := NewScheduler(registry, dispatcher, collector)

	// Resolve GitHub token: prefer App installation token, fall back to PAT.
	ghToken := resolveGitHubToken()

	// Wire feedback handler if a GitHub token is available.
	if ghToken != "" {
		scheduler.SetFeedback(NewFeedbackHandler(ghToken))
		log.Printf("feedback handler enabled (issue creation/closure)")
	}

	// Wire publisher for Discussion creation.
	if ghToken != "" {
		repoOwner := envOrDefault("GITHUB_REPO_OWNER", "tinyland-inc")
		repoName := envOrDefault("GITHUB_REPO_NAME", "remote-juggler")
		pub := NewPublisher(ghToken, repoOwner, repoName)
		if err := pub.Init(context.Background()); err != nil {
			log.Printf("publisher init failed (discussions disabled): %v", err)
		} else {
			scheduler.SetPublisher(pub)
			log.Printf("publisher enabled (discussions to %s/%s)", repoOwner, repoName)
		}
	}

	// Wire App token provider for automatic refresh (1h TTL tokens).
	if appTokenProvider != nil {
		scheduler.SetTokenProvider(appTokenProvider)
		log.Printf("app token auto-refresh enabled")
	}

	// Run a specific campaign and exit.
	if *campaignID != "" {
		campaign, ok := registry[*campaignID]
		if !ok {
			log.Fatalf("campaign %q not found in registry", *campaignID)
		}
		ctx, cancel := context.WithTimeout(context.Background(), parseDuration(campaign.Guardrails.MaxDuration))
		defer cancel()
		if err := scheduler.RunCampaign(ctx, campaign); err != nil {
			log.Fatalf("campaign %s failed: %v", *campaignID, err)
		}
		return
	}

	// Run all due campaigns once and exit.
	if *once {
		ctx := context.Background()
		scheduler.RunDue(ctx)
		return
	}

	// Start HTTP API server for manual triggering and status queries.
	if *apiPort > 0 {
		api := NewAPIServer(scheduler, registry)
		scheduler.OnResult = api.RecordResult
		go func() {
			addr := fmt.Sprintf(":%d", *apiPort)
			if err := api.ListenAndServe(addr); err != nil {
				log.Printf("api server error: %v", err)
			}
		}()
	}

	// Main loop: check for due campaigns on interval.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan os.Signal, 1)
	signal.Notify(done, syscall.SIGINT, syscall.SIGTERM)

	log.Printf("starting scheduler loop (interval=%s, %d campaigns)", *interval, len(registry))

	// Startup: clear stale kill switch from previous deployment.
	// Setec PVC persists kill switch state across pod restarts. Without this,
	// every deploy blocks ALL campaigns until manual intervention.
	if collector != nil {
		if killed, err := collector.CheckKillSwitch(ctx); err != nil {
			log.Printf("startup: kill switch check failed: %v (continuing)", err)
		} else if killed {
			log.Printf("startup: kill switch active from previous deployment, auto-clearing")
			if err := collector.ClearKillSwitch(ctx); err != nil {
				log.Printf("startup: failed to clear kill switch: %v", err)
			} else {
				log.Printf("startup: kill switch cleared successfully")
			}
		}
	}

	// Post-deploy smoke test: run cc-gateway-health to validate the
	// gateway, Setec, and dispatch pipeline before entering the main loop.
	if smokeTest, ok := registry["cc-gateway-health"]; ok {
		log.Printf("startup: running post-deploy smoke test (cc-gateway-health)")
		smokeCtx, smokeCancel := context.WithTimeout(ctx, 2*time.Minute)
		if err := scheduler.RunCampaign(smokeCtx, smokeTest); err != nil {
			log.Printf("startup: smoke test failed: %v (non-fatal, continuing)", err)
		} else {
			log.Printf("startup: smoke test passed")
		}
		smokeCancel()
	}

	ticker := time.NewTicker(*interval)
	defer ticker.Stop()

	// ConfigMap hot-reload: re-scan campaigns directory periodically.
	// K8s updates ConfigMaps in-place (symlink swap), so we re-read
	// the directory to pick up new/changed campaign definitions without
	// requiring a pod restart.
	reloadTicker := time.NewTicker(5 * time.Minute)
	defer reloadTicker.Stop()

	// Run immediately on start.
	scheduler.RunDue(ctx)

	for {
		select {
		case <-ticker.C:
			scheduler.RunDue(ctx)
		case <-reloadTicker.C:
			updated := loadCampaigns(*campaignsDir)
			if len(updated) != len(registry) {
				log.Printf("hot-reload: campaign count changed %d -> %d", len(registry), len(updated))
			}
			registry = updated
			scheduler.UpdateRegistry(updated)
		case <-done:
			log.Println("shutting down")
			cancel()
			return
		}
	}
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func intEnvOrDefault(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func parseDuration(s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 30 * time.Minute // safe default
	}
	return d
}

// CampaignIndex is the registry of all campaigns.
type CampaignIndex struct {
	Version   string                   `json:"version"`
	Campaigns map[string]CampaignEntry `json:"campaigns"`
}

// CampaignEntry is a single entry in the campaign index.
type CampaignEntry struct {
	File       string  `json:"file"`
	Enabled    bool    `json:"enabled"`
	LastRun    *string `json:"lastRun"`
	LastResult *string `json:"lastResult"`
}

// appTokenProvider is the singleton App token provider, initialized on first use.
var appTokenProvider *AppTokenProvider

// resolveGitHubToken returns a GitHub token using this priority:
//  1. GITHUB_APP_TOKEN (pre-resolved by init container or entrypoint)
//  2. GITHUB_APP_ID + GITHUB_APP_PRIVATE_KEY (generate JWT, exchange for installation token)
//  3. GITHUB_TOKEN (PAT — attributed to the PAT owner, not the App bot)
func resolveGitHubToken() string {
	// Pre-resolved App installation token (highest priority).
	if t := os.Getenv("GITHUB_APP_TOKEN"); t != "" {
		log.Printf("using pre-resolved GitHub App installation token")
		return t
	}

	// Generate installation token from App credentials (Discussions attributed to bot).
	if os.Getenv("GITHUB_APP_ID") != "" && os.Getenv("GITHUB_APP_PRIVATE_KEY") != "" {
		provider, err := NewAppTokenProvider()
		if err != nil {
			log.Printf("github-app: init failed: %v (falling back to PAT)", err)
		} else {
			token, err := provider.Token()
			if err != nil {
				log.Printf("github-app: token generation failed: %v (falling back to PAT)", err)
			} else {
				appTokenProvider = provider
				log.Printf("using GitHub App installation token (bot attribution)")
				return token
			}
		}
	}

	// Standard PAT (fallback — attributed to PAT owner).
	if t := os.Getenv("GITHUB_TOKEN"); t != "" {
		log.Printf("using GitHub PAT (Discussions attributed to PAT owner, not bot)")
		return t
	}
	return ""
}

// loadCampaigns loads all enabled campaign definitions from a directory.
// Returns a registry map keyed by campaign ID.
func loadCampaigns(dir string) map[string]*Campaign {
	indexPath := filepath.Join(dir, "index.json")
	index, err := LoadIndex(indexPath)
	if err != nil {
		log.Printf("load index: %v", err)
		return nil
	}
	log.Printf("loaded %d campaigns from %s", len(index.Campaigns), indexPath)

	registry := make(map[string]*Campaign)
	for id, entry := range index.Campaigns {
		if !entry.Enabled {
			continue
		}
		defPath := filepath.Join(dir, entry.File)
		// Fallback: ConfigMap mounts files flat (no subdirectories),
		// but index.json references paths like "gateway-direct/cc-gateway-health.json".
		// Try the basename if the full path doesn't exist.
		if _, statErr := os.Stat(defPath); os.IsNotExist(statErr) {
			defPath = filepath.Join(dir, filepath.Base(entry.File))
		}
		campaign, err := LoadCampaign(defPath)
		if err != nil {
			log.Printf("campaign %s: load error: %v", id, err)
			continue
		}
		if campaign.ID != id {
			log.Printf("campaign %s: ID mismatch (file says %s)", id, campaign.ID)
			continue
		}
		registry[id] = campaign
		log.Printf("campaign %s: loaded (%s, agent=%s)", id, campaign.Name, campaign.Agent)
	}
	return registry
}

// LoadIndex loads the campaign index from a JSON file.
func LoadIndex(path string) (*CampaignIndex, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var index CampaignIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, err
	}
	return &index, nil
}
