package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadCampaign(t *testing.T) {
	// Use the actual campaign definitions from the repo.
	defs := []string{
		"../gateway-direct/cc-mcp-regression.json",
		"../openclaw/oc-dep-audit.json",
		"../cross-agent/xa-audit-completeness.json",
	}
	for _, path := range defs {
		t.Run(filepath.Base(path), func(t *testing.T) {
			c, err := LoadCampaign(path)
			if err != nil {
				t.Fatalf("LoadCampaign(%s): %v", path, err)
			}
			if c.ID == "" {
				t.Error("campaign ID is empty")
			}
			if c.Agent == "" {
				t.Error("campaign agent is empty")
			}
			if len(c.Tools) == 0 {
				t.Error("campaign has no tools")
			}
			if c.Guardrails.MaxDuration == "" {
				t.Error("campaign has no maxDuration guardrail")
			}
		})
	}
}

func TestLoadIndex(t *testing.T) {
	index, err := LoadIndex("../index.json")
	if err != nil {
		t.Fatalf("LoadIndex: %v", err)
	}
	if index.Version == "" {
		t.Error("index version is empty")
	}
	if len(index.Campaigns) == 0 {
		t.Error("index has no campaigns")
	}
	for id, entry := range index.Campaigns {
		if entry.File == "" {
			t.Errorf("campaign %s has no file", id)
		}
	}
}

func TestLoadIndexMissing(t *testing.T) {
	_, err := LoadIndex("/nonexistent/index.json")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestCronMatches(t *testing.T) {
	tests := []struct {
		name string
		expr string
		time time.Time
		want bool
	}{
		{
			name: "exact match",
			expr: "0 4 1 1 *",
			time: time.Date(2026, 1, 1, 4, 0, 0, 0, time.UTC),
			want: true,
		},
		{
			name: "all wildcards",
			expr: "* * * * *",
			time: time.Now(),
			want: true,
		},
		{
			name: "wrong minute",
			expr: "30 4 * * *",
			time: time.Date(2026, 1, 1, 4, 0, 0, 0, time.UTC),
			want: false,
		},
		{
			name: "wrong hour",
			expr: "0 2 * * 1",
			time: time.Date(2026, 1, 1, 4, 0, 0, 0, time.UTC), // hour=4, want 2
			want: false,
		},
		{
			name: "weekly monday 2am",
			expr: "0 2 * * 1",
			time: time.Date(2026, 2, 23, 2, 0, 0, 0, time.UTC), // Monday
			want: true,
		},
		{
			name: "monthly 1st 4am",
			expr: "0 4 1 * *",
			time: time.Date(2026, 3, 1, 4, 0, 0, 0, time.UTC),
			want: true,
		},
		{
			name: "comma day-of-week tue,fri on tuesday",
			expr: "0 10 * * 2,5",
			time: time.Date(2026, 3, 3, 10, 0, 0, 0, time.UTC), // Tuesday
			want: true,
		},
		{
			name: "comma day-of-week tue,fri on friday",
			expr: "0 10 * * 2,5",
			time: time.Date(2026, 3, 6, 10, 0, 0, 0, time.UTC), // Friday
			want: true,
		},
		{
			name: "comma day-of-week tue,fri on wednesday",
			expr: "0 10 * * 2,5",
			time: time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC), // Wednesday
			want: false,
		},
		{
			name: "comma day-of-month 1st,15th on 1st",
			expr: "0 3 1,15 * *",
			time: time.Date(2026, 3, 1, 3, 0, 0, 0, time.UTC),
			want: true,
		},
		{
			name: "comma day-of-month 1st,15th on 15th",
			expr: "0 3 1,15 * *",
			time: time.Date(2026, 3, 15, 3, 0, 0, 0, time.UTC),
			want: true,
		},
		{
			name: "comma day-of-month 1st,15th on 10th",
			expr: "0 3 1,15 * *",
			time: time.Date(2026, 3, 10, 3, 0, 0, 0, time.UTC),
			want: false,
		},
		{
			name: "step every 5 minutes at 0",
			expr: "*/5 * * * *",
			time: time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC),
			want: true,
		},
		{
			name: "step every 5 minutes at 15",
			expr: "*/5 * * * *",
			time: time.Date(2026, 3, 1, 12, 15, 0, 0, time.UTC),
			want: true,
		},
		{
			name: "step every 5 minutes at 3",
			expr: "*/5 * * * *",
			time: time.Date(2026, 3, 1, 12, 3, 0, 0, time.UTC),
			want: false,
		},
		{
			name: "invalid expression",
			expr: "bad cron",
			time: time.Now(),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cronMatches(tt.expr, tt.time)
			if got != tt.want {
				t.Errorf("cronMatches(%q, %v) = %v, want %v", tt.expr, tt.time, got, tt.want)
			}
		})
	}
}

func TestIsDue(t *testing.T) {
	scheduler := NewScheduler(nil, nil, nil)

	tests := []struct {
		name     string
		campaign *Campaign
		want     bool
	}{
		{
			name: "manual only",
			campaign: &Campaign{
				ID:      "manual-only",
				Trigger: CampaignTrigger{Event: "manual"},
			},
			want: false,
		},
		{
			name: "push event",
			campaign: &Campaign{
				ID:      "push-triggered",
				Trigger: CampaignTrigger{Event: "push"},
			},
			want: false,
		},
		{
			name: "no trigger",
			campaign: &Campaign{
				ID:      "no-trigger",
				Trigger: CampaignTrigger{},
			},
			want: false,
		},
	}

	now := time.Now().UTC()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := scheduler.isDue(tt.campaign, now)
			if got != tt.want {
				t.Errorf("isDue(%s) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		input string
		want  time.Duration
	}{
		{"5m", 5 * time.Minute},
		{"30m", 30 * time.Minute},
		{"1h", time.Hour},
		{"invalid", 30 * time.Minute}, // default
		{"", 30 * time.Minute},        // default
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseDuration(tt.input)
			if got != tt.want {
				t.Errorf("parseDuration(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestEnvOrDefault(t *testing.T) {
	// Unset env returns default.
	if got := envOrDefault("CAMPAIGN_TEST_NONEXISTENT_VAR", "fallback"); got != "fallback" {
		t.Errorf("expected 'fallback', got %q", got)
	}

	// Set env returns value.
	os.Setenv("CAMPAIGN_TEST_VAR", "override")
	defer os.Unsetenv("CAMPAIGN_TEST_VAR")
	if got := envOrDefault("CAMPAIGN_TEST_VAR", "fallback"); got != "override" {
		t.Errorf("expected 'override', got %q", got)
	}
}

func TestNewDispatcher(t *testing.T) {
	d := NewDispatcher("https://example.com", "http://ironclaw:8080", "http://tinyclaw:8080", "http://hexstrike:8080")
	if d.gatewayURL != "https://example.com" {
		t.Errorf("expected gateway URL 'https://example.com', got %q", d.gatewayURL)
	}
	if d.ironclawURL != "http://ironclaw:8080" {
		t.Errorf("expected ironclaw URL 'http://ironclaw:8080', got %q", d.ironclawURL)
	}
	if d.tinyclawURL != "http://tinyclaw:8080" {
		t.Errorf("expected tinyclaw URL 'http://tinyclaw:8080', got %q", d.tinyclawURL)
	}
	if d.hexstrikeAIURL != "http://hexstrike:8080" {
		t.Errorf("expected hexstrike-ai URL 'http://hexstrike:8080', got %q", d.hexstrikeAIURL)
	}
	if d.httpClient == nil {
		t.Error("httpClient is nil")
	}
}

func TestNewCollector(t *testing.T) {
	c := NewCollector("https://example.com")
	if c.gatewayURL != "https://example.com" {
		t.Errorf("expected gateway URL 'https://example.com', got %q", c.gatewayURL)
	}
	if c.dispatcher == nil {
		t.Error("dispatcher is nil")
	}
}

func TestIsDueDependsOnSkippedInPass1(t *testing.T) {
	scheduler := NewScheduler(nil, nil, nil)
	campaign := &Campaign{
		ID: "dep-campaign",
		Trigger: CampaignTrigger{
			Schedule:  "* * * * *",
			DependsOn: []string{"prerequisite"},
		},
	}
	// Even with a matching schedule, campaigns with dependsOn should not trigger in pass 1.
	got := scheduler.isDue(campaign, time.Now().UTC())
	if got {
		t.Error("isDue should return false for campaigns with dependsOn")
	}
}

func TestDependenciesMetAllSatisfied(t *testing.T) {
	scheduler := NewScheduler(nil, nil, nil)
	scheduler.completedRuns["campaign-a"] = true
	scheduler.completedRuns["campaign-b"] = true

	campaign := &Campaign{
		ID: "dependent",
		Trigger: CampaignTrigger{
			DependsOn: []string{"campaign-a", "campaign-b"},
		},
	}
	if !scheduler.dependenciesMet(campaign) {
		t.Error("dependenciesMet should return true when all deps are met")
	}
}

func TestDependenciesMetPartiallyUnsatisfied(t *testing.T) {
	scheduler := NewScheduler(nil, nil, nil)
	scheduler.completedRuns["campaign-a"] = true
	// campaign-b NOT completed.

	campaign := &Campaign{
		ID: "dependent",
		Trigger: CampaignTrigger{
			DependsOn: []string{"campaign-a", "campaign-b"},
		},
	}
	if scheduler.dependenciesMet(campaign) {
		t.Error("dependenciesMet should return false when not all deps are met")
	}
}

func TestDependenciesMetNoDeps(t *testing.T) {
	scheduler := NewScheduler(nil, nil, nil)
	campaign := &Campaign{
		ID:      "no-deps",
		Trigger: CampaignTrigger{},
	}
	if !scheduler.dependenciesMet(campaign) {
		t.Error("dependenciesMet should return true when there are no deps")
	}
}

func TestMarkCompleted(t *testing.T) {
	scheduler := NewScheduler(nil, nil, nil)
	scheduler.MarkCompleted("test-campaign")
	if !scheduler.completedRuns["test-campaign"] {
		t.Error("MarkCompleted should mark campaign as completed")
	}
}
