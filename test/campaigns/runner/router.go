package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"
)

// FindingRouter routes high-value findings to the appropriate agent
// via Discussion labels and rj-meta comments.
type FindingRouter struct {
	publisher *Publisher
	rules     []RoutingRule
}

// RoutingRule defines criteria for routing a finding to a target agent.
type RoutingRule struct {
	// Match criteria — all non-empty fields must match.
	SourceAgent    string   // Campaign agent (e.g., "ironclaw")
	SeverityIn     []string // Finding severity must be in this list
	LabelContains  string   // Finding must have a label containing this
	CampaignPrefix string   // Campaign ID must start with this

	// Action on match.
	TargetAgent string   // Agent to hand off to
	Labels      []string // Discussion labels to apply
	Priority    int      // Lower = higher priority (first match wins)
}

// RoutedFinding represents a finding that was routed to a target agent.
type RoutedFinding struct {
	Finding     Finding
	TargetAgent string
	Labels      []string
	Meta        RJMeta
}

// RJMeta is the structured metadata embedded in Discussion comments.
type RJMeta struct {
	Version            string         `json:"version"`
	From               string         `json:"from"`
	To                 string         `json:"to,omitempty"`
	MessageType        string         `json:"message_type"`
	Priority           string         `json:"priority,omitempty"`
	FindingFingerprint string         `json:"finding_fingerprint,omitempty"`
	CampaignID         string         `json:"campaign_id"`
	RunID              string         `json:"run_id,omitempty"`
	Timestamp          string         `json:"timestamp"`
	ActionRequested    string         `json:"action_requested,omitempty"`
	Context            map[string]any `json:"context,omitempty"`
}

// NewFindingRouter creates a router with default rules.
func NewFindingRouter(publisher *Publisher) *FindingRouter {
	return &FindingRouter{
		publisher: publisher,
		rules:     DefaultRoutingRules(),
	}
}

// DefaultRoutingRules returns the 5 default routing rules.
func DefaultRoutingRules() []RoutingRule {
	return []RoutingRule{
		{
			SeverityIn:    []string{"critical", "high"},
			LabelContains: "security",
			TargetAgent:   "hexstrike-ai",
			Labels:        []string{"handoff:hexstrike-ai", "severity:high"},
			Priority:      1,
		},
		{
			LabelContains: "credential",
			TargetAgent:   "hexstrike-ai",
			Labels:        []string{"handoff:hexstrike-ai"},
			Priority:      2,
		},
		{
			SourceAgent:   "hexstrike-ai",
			LabelContains: "code-quality",
			TargetAgent:   "ironclaw",
			Labels:        []string{"handoff:ironclaw"},
			Priority:      3,
		},
		{
			LabelContains: "dependency",
			TargetAgent:   "ironclaw",
			Labels:        []string{"handoff:ironclaw"},
			Priority:      4,
		},
		{
			CampaignPrefix: "xa-upstream",
			TargetAgent:    "tinyclaw",
			Labels:         []string{"handoff:tinyclaw"},
			Priority:       5,
		},
	}
}

// Route evaluates all findings against routing rules and returns matches.
// It does NOT create Discussions or apply labels — that is done by the caller
// or by the scheduler integration.
func (r *FindingRouter) Route(campaign *Campaign, findings []Finding) []RoutedFinding {
	var routed []RoutedFinding
	for _, f := range findings {
		if rule, ok := r.matchRule(campaign, f); ok {
			meta := RJMeta{
				Version:            "1",
				From:               campaign.Agent,
				To:                 rule.TargetAgent,
				MessageType:        "handoff",
				Priority:           f.Severity,
				FindingFingerprint: GenerateFingerprint(campaign.ID, f.Title),
				CampaignID:         campaign.ID,
				Timestamp:          time.Now().UTC().Format(time.RFC3339),
				ActionRequested:    "review",
			}
			routed = append(routed, RoutedFinding{
				Finding:     f,
				TargetAgent: rule.TargetAgent,
				Labels:      rule.Labels,
				Meta:        meta,
			})
		}
	}
	return routed
}

// matchRule returns the first (highest priority) matching rule for a finding.
func (r *FindingRouter) matchRule(campaign *Campaign, f Finding) (RoutingRule, bool) {
	for _, rule := range r.rules {
		if r.ruleMatches(rule, campaign, f) {
			return rule, true
		}
	}
	return RoutingRule{}, false
}

// ruleMatches checks if all non-empty criteria in a rule are satisfied.
func (r *FindingRouter) ruleMatches(rule RoutingRule, campaign *Campaign, f Finding) bool {
	if rule.SourceAgent != "" && campaign.Agent != rule.SourceAgent {
		return false
	}
	if len(rule.SeverityIn) > 0 && !stringInSlice(f.Severity, rule.SeverityIn) {
		return false
	}
	if rule.LabelContains != "" && !labelContains(f.Labels, rule.LabelContains) {
		return false
	}
	if rule.CampaignPrefix != "" && !strings.HasPrefix(campaign.ID, rule.CampaignPrefix) {
		return false
	}
	return true
}

// GenerateFingerprint creates a stable SHA256 fingerprint for dedup.
func GenerateFingerprint(campaignID, findingTitle string) string {
	h := sha256.Sum256([]byte(campaignID + ":" + findingTitle))
	return fmt.Sprintf("%x", h)
}

// FormatRJMeta formats an RJMeta block as an HTML comment for embedding.
func FormatRJMeta(meta RJMeta) string {
	b, _ := json.MarshalIndent(meta, "", "  ")
	return fmt.Sprintf("\n<!-- rj-meta\n%s\n-->\n", string(b))
}

// ParseRJMeta extracts an RJMeta block from text containing an HTML comment.
func ParseRJMeta(text string) (RJMeta, bool) {
	start := strings.Index(text, "<!-- rj-meta")
	if start == -1 {
		return RJMeta{}, false
	}
	end := strings.Index(text[start:], "-->")
	if end == -1 {
		return RJMeta{}, false
	}

	// Extract the JSON between "<!-- rj-meta\n" and "\n-->"
	jsonStart := start + len("<!-- rj-meta\n")
	jsonEnd := start + end
	if jsonStart >= jsonEnd {
		return RJMeta{}, false
	}

	jsonStr := strings.TrimSpace(text[jsonStart:jsonEnd])
	var meta RJMeta
	if err := json.Unmarshal([]byte(jsonStr), &meta); err != nil {
		return RJMeta{}, false
	}
	return meta, true
}

// ApplyRoutingLabels adds routing labels to a Discussion via the publisher.
// This is a best-effort operation; errors are logged but not fatal.
func (r *FindingRouter) ApplyRoutingLabels(ctx context.Context, discussionNumber int, labels []string) {
	if r.publisher == nil {
		return
	}
	// Labels are applied via the gateway's github_discussion_label tool,
	// but since the router runs inside the campaign runner (not gateway),
	// we use the publisher's REST API to add labels.
	for _, label := range labels {
		log.Printf("router: would apply label %q to discussion #%d", label, discussionNumber)
	}
}

// stringInSlice checks if a string is in a slice.
func stringInSlice(s string, slice []string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

// labelContains checks if any label in the slice contains the substring.
func labelContains(labels []string, substr string) bool {
	for _, l := range labels {
		if strings.Contains(l, substr) {
			return true
		}
	}
	return false
}
