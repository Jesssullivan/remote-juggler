package main

import (
	_ "embed"
	"encoding/json"
	"net/http"
)

//go:embed portal.html
var portalHTML []byte

// handlePortal serves the unified agent portal dashboard.
// It aggregates agent health, campaign results, audit log, and Aperture metrics
// into a single HTML page accessible via the tailnet.
func handlePortal(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(portalHTML)
}

// handlePortalAPI returns JSON data for the portal's dynamic widgets.
// Called by the portal's JavaScript to populate agent status, campaigns, etc.
func handlePortalAPI(audit *AuditLog, meterStore *MeterStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agents := []map[string]any{
			{
				"name":    "IronClaw",
				"type":    "ironclaw",
				"url":     "https://ironclaw.taila4c78d.ts.net",
				"role":    "Code analysis, dependency audit, docs freshness",
				"adapter": true,
			},
			{
				"name":    "TinyClaw",
				"type":    "tinyclaw",
				"url":     "",
				"role":    "Lightweight scanning (dead code, TypeScript strict, a11y)",
				"adapter": true,
			},
			{
				"name":    "HexStrike-AI",
				"type":    "hexstrike-ai",
				"url":     "https://hexstrike.taila4c78d.ts.net",
				"role":    "Security testing, credential exposure, CVE monitoring",
				"adapter": true,
			},
		}

		recentAudit := audit.Recent(20)

		var usage map[string]any
		if meterStore != nil {
			usage = map[string]any{
				"total_buckets": len(meterStore.Snapshot()),
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"agents": agents,
			"audit":  recentAudit,
			"usage":  usage,
		})
	}
}
