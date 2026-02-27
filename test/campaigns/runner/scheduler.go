package main

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"
)

// Scheduler evaluates campaign triggers and orchestrates execution.
type Scheduler struct {
	registry      map[string]*Campaign
	dispatcher    *Dispatcher
	collector     *Collector
	feedback      *FeedbackHandler
	publisher     *Publisher
	tokenProvider *AppTokenProvider

	// completedRuns tracks successfully completed campaign IDs within the
	// current scheduler cycle. Used for dependsOn evaluation.
	completedRuns map[string]bool

	// OnResult is called after each campaign run with the result.
	// Used by the API server to track latest results.
	OnResult func(*CampaignResult)
}

// NewScheduler creates a Scheduler with the given campaign registry.
func NewScheduler(registry map[string]*Campaign, dispatcher *Dispatcher, collector *Collector) *Scheduler {
	return &Scheduler{
		registry:      registry,
		dispatcher:    dispatcher,
		collector:     collector,
		completedRuns: make(map[string]bool),
	}
}

// SetFeedback configures the feedback handler for issue creation/closure.
func (s *Scheduler) SetFeedback(handler *FeedbackHandler) {
	s.feedback = handler
}

// SetPublisher configures the publisher for Discussion creation.
func (s *Scheduler) SetPublisher(pub *Publisher) {
	s.publisher = pub
}

// SetTokenProvider configures an AppTokenProvider for automatic token refresh.
// When set, the scheduler refreshes tokens on publisher and feedback handler
// before each campaign dispatch.
func (s *Scheduler) SetTokenProvider(provider *AppTokenProvider) {
	s.tokenProvider = provider
}

// refreshTokens gets a fresh token from the AppTokenProvider and updates
// the publisher and feedback handler. Called before campaign dispatch.
func (s *Scheduler) refreshTokens() {
	if s.tokenProvider == nil {
		return
	}
	token, err := s.tokenProvider.Token()
	if err != nil {
		log.Printf("token refresh failed: %v", err)
		return
	}
	if s.publisher != nil {
		s.publisher.UpdateToken(token)
	}
	if s.feedback != nil {
		s.feedback.UpdateToken(token)
	}
}

// RunDue evaluates all campaigns and runs those whose triggers are satisfied.
// Uses two passes: first runs scheduled/manual campaigns, then runs dependent
// campaigns whose dependencies were satisfied in this cycle.
func (s *Scheduler) RunDue(ctx context.Context) {
	now := time.Now().UTC()

	// Pass 1: Run non-dependent campaigns.
	for id, campaign := range s.registry {
		if len(campaign.Trigger.DependsOn) > 0 {
			continue // Handle in pass 2.
		}
		if !s.isDue(campaign, now) {
			continue
		}
		log.Printf("campaign %s: trigger satisfied, dispatching", id)
		timeout := parseDuration(campaign.Guardrails.MaxDuration)
		runCtx, cancel := context.WithTimeout(ctx, timeout)
		if err := s.RunCampaign(runCtx, campaign); err != nil {
			log.Printf("campaign %s: failed: %v", id, err)
		} else {
			s.completedRuns[id] = true
		}
		cancel()
	}

	// Pass 2: Run dependent campaigns whose dependencies are now met.
	for id, campaign := range s.registry {
		if len(campaign.Trigger.DependsOn) == 0 {
			continue
		}
		if !s.dependenciesMet(campaign) {
			continue
		}
		log.Printf("campaign %s: dependencies met, dispatching", id)
		timeout := parseDuration(campaign.Guardrails.MaxDuration)
		runCtx, cancel := context.WithTimeout(ctx, timeout)
		if err := s.RunCampaign(runCtx, campaign); err != nil {
			log.Printf("campaign %s: failed: %v", id, err)
		} else {
			s.completedRuns[id] = true
		}
		cancel()
	}
}

// RunCampaign executes a single campaign end-to-end.
func (s *Scheduler) RunCampaign(ctx context.Context, campaign *Campaign) error {
	// Refresh App token if provider is configured (handles expiry).
	s.refreshTokens()

	runID := fmt.Sprintf("%s-%d", campaign.ID, time.Now().Unix())
	log.Printf("campaign %s: starting run %s (agent=%s, timeout=%s)",
		campaign.ID, runID, campaign.Agent, campaign.Guardrails.MaxDuration)

	// Check global kill switch (skip if collector not configured).
	if s.collector != nil {
		if killed, err := s.collector.CheckKillSwitch(ctx); err != nil {
			log.Printf("campaign %s: kill switch check error: %v (continuing)", campaign.ID, err)
		} else if killed {
			log.Printf("campaign %s: global kill switch active, skipping", campaign.ID)
			return fmt.Errorf("global kill switch active")
		}
	}

	result := &CampaignResult{
		CampaignID: campaign.ID,
		RunID:      runID,
		Agent:      campaign.Agent,
		StartedAt:  time.Now().UTC().Format(time.RFC3339),
	}

	// Dispatch to agent via rj-gateway MCP.
	if s.dispatcher == nil {
		result.Status = "error"
		result.Error = "no dispatcher configured"
		result.FinishedAt = time.Now().UTC().Format(time.RFC3339)
		s.storeResult(ctx, campaign, result)
		return fmt.Errorf("no dispatcher configured")
	}
	dispatchResult, err := s.dispatcher.Dispatch(ctx, campaign, runID)
	if err != nil {
		result.Status = "error"
		result.Error = err.Error()
		result.FinishedAt = time.Now().UTC().Format(time.RFC3339)
		s.storeResult(ctx, campaign, result)
		return err
	}

	result.ToolCalls = dispatchResult.ToolCalls
	result.KPIs = dispatchResult.KPIs
	result.ToolTrace = dispatchResult.ToolTrace
	result.Findings = dispatchResult.Findings

	if ctx.Err() != nil {
		result.Status = "timeout"
		result.Error = ctx.Err().Error()
	} else if dispatchResult.Error != "" {
		result.Status = "failure"
		result.Error = dispatchResult.Error
	} else {
		result.Status = "success"
	}

	result.FinishedAt = time.Now().UTC().Format(time.RFC3339)

	log.Printf("campaign %s: run %s completed (status=%s, tools=%d)",
		campaign.ID, runID, result.Status, result.ToolCalls)

	s.storeResult(ctx, campaign, result)
	return nil
}

// storeResult persists the campaign result via the collector, processes
// feedback (issue creation/closure), publishes to Discussions, and notifies observers.
func (s *Scheduler) storeResult(ctx context.Context, campaign *Campaign, result *CampaignResult) {
	if s.collector != nil {
		if err := s.collector.StoreResult(ctx, campaign, result); err != nil {
			log.Printf("campaign %s: failed to store result: %v", campaign.ID, err)
		}
	}
	if s.feedback != nil && len(result.Findings) > 0 {
		// Load previous findings for issue-closing comparison.
		var previousFindings []Finding
		if campaign.Feedback.CloseResolvedIssues && s.collector != nil {
			previousFindings = s.collector.GetPreviousFindings(ctx, campaign)
		}
		if err := s.feedback.ProcessFindings(ctx, campaign, result.Findings, previousFindings); err != nil {
			log.Printf("campaign %s: feedback error: %v", campaign.ID, err)
		}
	}
	if s.publisher != nil && campaign.Feedback.ShouldPublish(result.Status) {
		url, err := s.publisher.Publish(ctx, campaign, result)
		if err != nil {
			log.Printf("campaign %s: publish error: %v", campaign.ID, err)
		} else {
			result.DiscussionURL = url
			// Update Setec with the Discussion URL.
			if s.collector != nil {
				if err := s.collector.StoreResult(ctx, campaign, result); err != nil {
					log.Printf("campaign %s: failed to update result with discussion URL: %v", campaign.ID, err)
				}
			}
		}
	}
	if s.OnResult != nil {
		s.OnResult(result)
	}
}

// isDue checks if a campaign's trigger is satisfied at the given time.
func (s *Scheduler) isDue(campaign *Campaign, now time.Time) bool {
	trigger := campaign.Trigger

	// Manual-only campaigns never auto-trigger.
	if trigger.Schedule == "" && trigger.Event == "manual" {
		return false
	}

	// Event-triggered campaigns (push, PR) are dispatched externally.
	if trigger.Event == "push" || trigger.Event == "pull_request" {
		return false
	}

	// Dependent campaigns are handled separately in RunDue pass 2.
	if len(trigger.DependsOn) > 0 {
		return false
	}

	// Evaluate cron schedule.
	if trigger.Schedule != "" {
		return cronMatches(trigger.Schedule, now)
	}

	return false
}

// dependenciesMet checks whether all campaigns in the DependsOn list have
// completed successfully in the current scheduler cycle.
func (s *Scheduler) dependenciesMet(campaign *Campaign) bool {
	for _, dep := range campaign.Trigger.DependsOn {
		if !s.completedRuns[dep] {
			return false
		}
	}
	return true
}

// MarkCompleted records a campaign ID as having completed successfully.
// Useful for external triggers (webhooks, manual) that bypass the scheduler.
func (s *Scheduler) MarkCompleted(campaignID string) {
	s.completedRuns[campaignID] = true
}

// cronMatches evaluates a simple 5-field cron expression against a time.
// Format: minute hour day-of-month month day-of-week
// Supports: numbers, * (any), and ranges are not implemented (keep it simple).
func cronMatches(expr string, t time.Time) bool {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return false
	}

	checks := []struct {
		field string
		value int
	}{
		{fields[0], t.Minute()},
		{fields[1], t.Hour()},
		{fields[2], t.Day()},
		{fields[3], int(t.Month())},
		{fields[4], int(t.Weekday())}, // 0=Sunday
	}

	for _, c := range checks {
		if !cronFieldMatches(c.field, c.value) {
			return false
		}
	}
	return true
}

// cronFieldMatches checks if a single cron field matches a value.
func cronFieldMatches(field string, value int) bool {
	if field == "*" {
		return true
	}
	n, err := strconv.Atoi(field)
	if err != nil {
		return false
	}
	return n == value
}
