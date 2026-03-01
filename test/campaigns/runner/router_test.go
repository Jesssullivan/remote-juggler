package main

import (
	"strings"
	"testing"
)

func TestRouterMatchesSeverity(t *testing.T) {
	router := NewFindingRouter(nil)
	campaign := &Campaign{ID: "oc-dep-audit", Agent: "ironclaw"}
	findings := []Finding{
		{Title: "Critical vuln", Severity: "critical", Labels: []string{"security"}},
	}
	routed := router.Route(campaign, findings)
	if len(routed) != 1 {
		t.Fatalf("expected 1 routed finding, got %d", len(routed))
	}
	if routed[0].TargetAgent != "hexstrike-ai" {
		t.Errorf("expected target=hexstrike-ai, got %s", routed[0].TargetAgent)
	}
}

func TestRouterMatchesLabel(t *testing.T) {
	router := NewFindingRouter(nil)
	campaign := &Campaign{ID: "oc-identity-audit", Agent: "ironclaw"}
	findings := []Finding{
		{Title: "Exposed token", Severity: "high", Labels: []string{"credential-exposure"}},
	}
	routed := router.Route(campaign, findings)
	if len(routed) != 1 {
		t.Fatalf("expected 1 routed finding, got %d", len(routed))
	}
	// Should match rule 1 (severity:high + security-related) or rule 2 (credential).
	// Rule 1 requires "security" label, which is not present.
	// Rule 2 matches "credential" substring in labels.
	if routed[0].TargetAgent != "hexstrike-ai" {
		t.Errorf("expected target=hexstrike-ai, got %s", routed[0].TargetAgent)
	}
}

func TestRouterMatchesCampaignPrefix(t *testing.T) {
	router := NewFindingRouter(nil)
	campaign := &Campaign{ID: "xa-upstream-drift", Agent: "ironclaw"}
	findings := []Finding{
		{Title: "Version divergence", Severity: "medium", Labels: []string{"upstream"}},
	}
	routed := router.Route(campaign, findings)
	if len(routed) != 1 {
		t.Fatalf("expected 1 routed finding, got %d", len(routed))
	}
	if routed[0].TargetAgent != "tinyclaw" {
		t.Errorf("expected target=tinyclaw, got %s", routed[0].TargetAgent)
	}
}

func TestRouterMatchesSourceAgent(t *testing.T) {
	router := NewFindingRouter(nil)
	campaign := &Campaign{ID: "hs-scan", Agent: "hexstrike-ai"}
	findings := []Finding{
		{Title: "Code smell", Severity: "medium", Labels: []string{"code-quality"}},
	}
	routed := router.Route(campaign, findings)
	if len(routed) != 1 {
		t.Fatalf("expected 1 routed finding, got %d", len(routed))
	}
	if routed[0].TargetAgent != "ironclaw" {
		t.Errorf("expected target=ironclaw, got %s", routed[0].TargetAgent)
	}
}

func TestRouterFirstRuleWins(t *testing.T) {
	router := NewFindingRouter(nil)
	campaign := &Campaign{ID: "oc-dep-audit", Agent: "ironclaw"}
	// This finding matches rule 1 (severity:high + security label)
	// and also rule 4 (dependency label). Rule 1 should win.
	findings := []Finding{
		{Title: "Vulnerable dep", Severity: "high", Labels: []string{"security", "dependency"}},
	}
	routed := router.Route(campaign, findings)
	if len(routed) != 1 {
		t.Fatalf("expected 1 routed finding, got %d", len(routed))
	}
	if routed[0].TargetAgent != "hexstrike-ai" {
		t.Errorf("expected hexstrike-ai (rule 1 wins), got %s", routed[0].TargetAgent)
	}
}

func TestRouterNoMatch(t *testing.T) {
	router := NewFindingRouter(nil)
	campaign := &Campaign{ID: "oc-format-check", Agent: "ironclaw"}
	findings := []Finding{
		{Title: "Style issue", Severity: "low", Labels: []string{"style"}},
	}
	routed := router.Route(campaign, findings)
	if len(routed) != 0 {
		t.Errorf("expected 0 routed findings, got %d", len(routed))
	}
}

func TestRouterFormatsMetaComment(t *testing.T) {
	meta := RJMeta{
		Version:            "1",
		From:               "ironclaw",
		To:                 "hexstrike-ai",
		MessageType:        "handoff",
		Priority:           "high",
		FindingFingerprint: "abc123",
		CampaignID:         "oc-dep-audit",
		Timestamp:          "2026-02-28T12:00:00Z",
		ActionRequested:    "review",
	}
	formatted := FormatRJMeta(meta)

	if !strings.Contains(formatted, "<!-- rj-meta") {
		t.Error("missing rj-meta open tag")
	}
	if !strings.Contains(formatted, "-->") {
		t.Error("missing close tag")
	}
	if !strings.Contains(formatted, `"from": "ironclaw"`) {
		t.Error("missing from field")
	}
	if !strings.Contains(formatted, `"to": "hexstrike-ai"`) {
		t.Error("missing to field")
	}
}

func TestParseRJMeta_Valid(t *testing.T) {
	text := `Some discussion body text.

<!-- rj-meta
{
  "version": "1",
  "from": "ironclaw",
  "to": "hexstrike-ai",
  "message_type": "handoff",
  "campaign_id": "oc-dep-audit",
  "timestamp": "2026-02-28T12:00:00Z"
}
-->

More text after.`

	meta, ok := ParseRJMeta(text)
	if !ok {
		t.Fatal("expected ParseRJMeta to succeed")
	}
	if meta.Version != "1" {
		t.Errorf("version = %q, want 1", meta.Version)
	}
	if meta.From != "ironclaw" {
		t.Errorf("from = %q, want ironclaw", meta.From)
	}
	if meta.To != "hexstrike-ai" {
		t.Errorf("to = %q, want hexstrike-ai", meta.To)
	}
	if meta.CampaignID != "oc-dep-audit" {
		t.Errorf("campaign_id = %q, want oc-dep-audit", meta.CampaignID)
	}
}

func TestParseRJMeta_NoBlock(t *testing.T) {
	_, ok := ParseRJMeta("Just a regular comment with no metadata.")
	if ok {
		t.Error("expected ParseRJMeta to return false for text without rj-meta")
	}
}

func TestParseRJMeta_InvalidJSON(t *testing.T) {
	text := `<!-- rj-meta
{invalid json}
-->`
	_, ok := ParseRJMeta(text)
	if ok {
		t.Error("expected ParseRJMeta to return false for invalid JSON")
	}
}

func TestGenerateFingerprint_Stable(t *testing.T) {
	fp1 := GenerateFingerprint("oc-dep-audit", "CVE-2026-1234")
	fp2 := GenerateFingerprint("oc-dep-audit", "CVE-2026-1234")
	if fp1 != fp2 {
		t.Errorf("fingerprints should be stable: %s != %s", fp1, fp2)
	}
}

func TestGenerateFingerprint_Unique(t *testing.T) {
	fp1 := GenerateFingerprint("oc-dep-audit", "CVE-2026-1234")
	fp2 := GenerateFingerprint("oc-dep-audit", "CVE-2026-5678")
	if fp1 == fp2 {
		t.Error("different findings should produce different fingerprints")
	}
}

func TestRouterMetaPopulation(t *testing.T) {
	router := NewFindingRouter(nil)
	campaign := &Campaign{ID: "oc-dep-audit", Agent: "ironclaw"}
	findings := []Finding{
		{Title: "Critical vuln", Severity: "critical", Labels: []string{"security"}},
	}
	routed := router.Route(campaign, findings)
	if len(routed) != 1 {
		t.Fatalf("expected 1 routed, got %d", len(routed))
	}

	meta := routed[0].Meta
	if meta.Version != "1" {
		t.Errorf("version = %q, want 1", meta.Version)
	}
	if meta.From != "ironclaw" {
		t.Errorf("from = %q, want ironclaw", meta.From)
	}
	if meta.To != "hexstrike-ai" {
		t.Errorf("to = %q, want hexstrike-ai", meta.To)
	}
	if meta.MessageType != "handoff" {
		t.Errorf("message_type = %q, want handoff", meta.MessageType)
	}
	if meta.CampaignID != "oc-dep-audit" {
		t.Errorf("campaign_id = %q, want oc-dep-audit", meta.CampaignID)
	}
	if meta.FindingFingerprint == "" {
		t.Error("finding_fingerprint should not be empty")
	}
}

func TestRouterMultipleFindings(t *testing.T) {
	router := NewFindingRouter(nil)
	campaign := &Campaign{ID: "oc-dep-audit", Agent: "ironclaw"}
	findings := []Finding{
		{Title: "Critical vuln", Severity: "critical", Labels: []string{"security"}},
		{Title: "Style issue", Severity: "low", Labels: []string{"style"}},
		{Title: "Dep bump", Severity: "medium", Labels: []string{"dependency"}},
	}
	routed := router.Route(campaign, findings)
	if len(routed) != 2 {
		t.Fatalf("expected 2 routed (security + dependency), got %d", len(routed))
	}
	if routed[0].TargetAgent != "hexstrike-ai" {
		t.Errorf("first routed should be hexstrike-ai, got %s", routed[0].TargetAgent)
	}
	if routed[1].TargetAgent != "ironclaw" {
		t.Errorf("second routed should be ironclaw, got %s", routed[1].TargetAgent)
	}
}
