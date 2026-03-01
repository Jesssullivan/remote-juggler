package main

import (
	"encoding/json"
	"os"
)

// Campaign is a full campaign definition loaded from JSON.
type Campaign struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Agent       string          `json:"agent"`
	Model       string          `json:"model,omitempty"`
	Mode        string          `json:"mode,omitempty"` // "schema" or "autonomous"
	Trigger     CampaignTrigger `json:"trigger"`
	Targets     []Target        `json:"targets"`
	Tools       []string        `json:"tools"`
	Process     []string        `json:"process"`
	Outputs     CampaignOutputs `json:"outputs"`
	Guardrails  Guardrails      `json:"guardrails"`
	Feedback    Feedback        `json:"feedback"`
	Metrics     Metrics         `json:"metrics"`
}

// CampaignTrigger defines when a campaign should run.
type CampaignTrigger struct {
	Schedule    string   `json:"schedule,omitempty"`
	Event       string   `json:"event,omitempty"`
	DependsOn   []string `json:"dependsOn,omitempty"`
	PathFilters []string `json:"pathFilters,omitempty"`
}

// Target identifies a forge/org/repo/branch tuple.
type Target struct {
	Forge  string `json:"forge"`
	Org    string `json:"org"`
	Repo   string `json:"repo"`
	Branch string `json:"branch"`
}

// CampaignOutputs describes where campaign results are stored.
type CampaignOutputs struct {
	SetecKey       string   `json:"setecKey"`
	IssueLabels    []string `json:"issueLabels,omitempty"`
	IssueRepo      string   `json:"issueRepo,omitempty"`
	PRBranchPrefix string   `json:"prBranchPrefix,omitempty"`
	PRBodyTemplate string   `json:"prBodyTemplate,omitempty"`
}

// Guardrails define safety constraints for campaign execution.
type Guardrails struct {
	MaxDuration string    `json:"maxDuration"`
	ReadOnly    bool      `json:"readOnly"`
	KillSwitch  string    `json:"killSwitch,omitempty"`
	AIApiBudget *AIBudget `json:"aiApiBudget,omitempty"`
}

// AIBudget caps AI API usage per campaign run.
type AIBudget struct {
	MaxTokens int `json:"maxTokens"`
}

// Feedback defines how campaign results feed back into the org.
type Feedback struct {
	CreateIssues        bool  `json:"createIssues"`
	CreatePRs           bool  `json:"createPRs"`
	CloseResolvedIssues bool  `json:"closeResolvedIssues"`
	PublishOnSuccess    *bool `json:"publishOnSuccess,omitempty"` // nil = true (default: always publish)
}

// ShouldPublish returns whether a Discussion should be created for the given result status.
// When PublishOnSuccess is explicitly false, only non-success results are published.
func (f Feedback) ShouldPublish(status string) bool {
	if f.PublishOnSuccess != nil && !*f.PublishOnSuccess {
		return status != "success"
	}
	return true
}

// Metrics defines success criteria and KPIs.
type Metrics struct {
	SuccessCriteria string   `json:"successCriteria"`
	KPIs            []string `json:"kpis"`
}

// ToolTraceEntry records a single tool invocation during campaign execution.
type ToolTraceEntry struct {
	Timestamp string `json:"timestamp"`
	Tool      string `json:"tool"`
	Summary   string `json:"summary"`
	IsError   bool   `json:"is_error,omitempty"`
}

// CampaignResult captures the outcome of a campaign run.
type CampaignResult struct {
	CampaignID    string           `json:"campaign_id"`
	RunID         string           `json:"run_id"`
	Status        string           `json:"status"` // "success", "failure", "timeout", "error", "budget_exceeded"
	StartedAt     string           `json:"started_at"`
	FinishedAt    string           `json:"finished_at"`
	Agent         string           `json:"agent"`
	KPIs          map[string]any   `json:"kpis,omitempty"`
	Error         string           `json:"error,omitempty"`
	ToolCalls     int              `json:"tool_calls"`
	TokensUsed    int              `json:"tokens_used,omitempty"`
	Phases        []PhaseResult    `json:"phases,omitempty"`
	Findings      []Finding        `json:"findings,omitempty"`
	ToolTrace     []ToolTraceEntry `json:"tool_trace,omitempty"`
	DiscussionURL string           `json:"discussion_url,omitempty"`
}

// PhaseResult captures the outcome of a single phase in a multi-phase campaign.
type PhaseResult struct {
	Phase     int    `json:"phase"`
	Agent     string `json:"agent"`
	Status    string `json:"status"`
	ToolCalls int    `json:"tool_calls"`
	Error     string `json:"error,omitempty"`
}

// LoadCampaign loads a campaign definition from a JSON file.
func LoadCampaign(path string) (*Campaign, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Campaign
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	return &c, nil
}
