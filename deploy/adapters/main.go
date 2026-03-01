// Campaign adapter sidecar — bridges real FOSS agent HTTP APIs to the
// campaign runner's dispatch protocol (POST /campaign, GET /status, GET /health).
//
// Each agent type has a different native API; the adapter translates between
// the campaign runner's expectations and the agent's actual endpoints.
//
// Usage:
//
//	adapter --agent-type=ironclaw --agent-url=http://localhost:18789 --listen-port=8080
//	adapter --agent-type=tinyclaw --agent-url=http://localhost:18790 --listen-port=8080 --gateway-url=http://rj-gateway:8080
//	adapter --agent-type=hexstrike-ai --agent-url=http://localhost:8888 --listen-port=8080
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
)

func main() {
	agentType := flag.String("agent-type", envOrDefault("ADAPTER_AGENT_TYPE", ""), "agent type: ironclaw, tinyclaw, hexstrike-ai")
	agentURL := flag.String("agent-url", envOrDefault("ADAPTER_AGENT_URL", ""), "base URL of the agent container")
	listenPort := flag.Int("listen-port", intEnvOrDefault("ADAPTER_LISTEN_PORT", 8080), "HTTP port to listen on")
	gatewayURL := flag.String("gateway-url", envOrDefault("ADAPTER_GATEWAY_URL", ""), "rj-gateway URL (for tool proxy)")
	agentAuthToken := flag.String("agent-auth-token", envOrDefault("ADAPTER_AGENT_AUTH_TOKEN", ""), "bearer token for agent API auth (OpenClaw gateway)")
	skillsDir := flag.String("skills-dir", envOrDefault("ADAPTER_SKILLS_DIR", ""), "path to workspace/skills/ directory for skill injection")
	flag.Parse()

	if *agentType == "" || *agentURL == "" {
		fmt.Fprintln(os.Stderr, "required: --agent-type and --agent-url")
		flag.Usage()
		os.Exit(1)
	}

	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix(fmt.Sprintf("adapter[%s]: ", *agentType))

	adapter, err := NewAdapter(*agentType, *agentURL, *gatewayURL, *agentAuthToken, *skillsDir)
	if err != nil {
		log.Fatalf("init adapter: %v", err)
	}

	addr := fmt.Sprintf(":%d", *listenPort)
	log.Printf("listening on %s (agent=%s, url=%s)", addr, *agentType, *agentURL)

	if err := http.ListenAndServe(addr, adapter); err != nil {
		log.Fatalf("server: %v", err)
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
