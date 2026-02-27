package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// PicoclawBackend translates campaign requests to TinyClaw's HTTP dispatch API.
// TinyClaw (formerly PicoClaw) exposes POST /api/dispatch for agent task execution,
// GET /api/tools for tool listing, and GET /api/status for health/status info.
type PicoclawBackend struct {
	agentURL   string
	gatewayURL string
	skillsDir  string // Path to workspace/skills/ directory (optional)
	httpClient *http.Client
}

func NewPicoclawBackend(agentURL, gatewayURL string) *PicoclawBackend {
	return &PicoclawBackend{
		agentURL:   agentURL,
		gatewayURL: gatewayURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Minute, // TinyClaw has 10m write deadline on dispatch
		},
	}
}

// SetSkillsDir sets the path to the workspace skills directory.
// SKILL.md files under this directory are loaded and injected into campaign prompts.
func (b *PicoclawBackend) SetSkillsDir(dir string) {
	b.skillsDir = dir
}

// loadSkills reads all SKILL.md files from the skills directory and returns
// their content concatenated as context for the LLM prompt.
func (b *PicoclawBackend) loadSkills() string {
	if b.skillsDir == "" {
		return ""
	}

	entries, err := os.ReadDir(b.skillsDir)
	if err != nil {
		log.Printf("skills: cannot read %s: %v", b.skillsDir, err)
		return ""
	}

	var parts []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		skillFile := filepath.Join(b.skillsDir, e.Name(), "SKILL.md")
		data, err := os.ReadFile(skillFile)
		if err != nil {
			continue
		}
		content := strings.TrimSpace(string(data))
		if content != "" {
			parts = append(parts, content)
		}
	}

	if len(parts) == 0 {
		return ""
	}

	log.Printf("skills: loaded %d skill(s) from %s", len(parts), b.skillsDir)
	return "\n\n## Skills Reference\n\n" + strings.Join(parts, "\n\n---\n\n")
}

func (b *PicoclawBackend) Type() string { return "picoclaw" }

func (b *PicoclawBackend) Health() error {
	resp, err := b.httpClient.Get(b.agentURL + "/api/status")
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return nil
		}
	}
	// Fall back to legacy /health endpoint.
	resp, err = b.httpClient.Get(b.agentURL + "/health")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("tinyclaw health: %d", resp.StatusCode)
	}
	return nil
}

// Dispatch sends a campaign to TinyClaw via POST /api/dispatch.
// TinyClaw accepts tasks with {content, session_key, channel} and returns
// {content, finish_reason, error}.
func (b *PicoclawBackend) Dispatch(campaign json.RawMessage, runID string) (*LastResult, error) {
	var c struct {
		ID      string   `json:"id"`
		Name    string   `json:"name"`
		Process []string `json:"process"`
		Tools   []string `json:"tools"`
		Model   string   `json:"model"`
	}
	if err := json.Unmarshal(campaign, &c); err != nil {
		return nil, fmt.Errorf("parse campaign: %w", err)
	}

	// Build prompt from campaign process steps.
	prompt := fmt.Sprintf("Campaign: %s (run_id: %s)\n\n", c.Name, runID)
	for i, step := range c.Process {
		prompt += fmt.Sprintf("%d. %s\n", i+1, step)
	}

	// Inject workspace skills as reference context.
	prompt += b.loadSkills()

	prompt += findingsInstruction

	// TinyClaw dispatch API.
	payload := map[string]string{
		"content":     prompt,
		"session_key": runID,
		"channel":     "campaign",
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, b.agentURL+"/api/dispatch", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tinyclaw dispatch: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return &LastResult{
			Status: "failure",
			Error:  fmt.Sprintf("tinyclaw returned %d: %s", resp.StatusCode, string(respBody)),
		}, nil
	}

	// Parse TinyClaw dispatch response.
	var dispatchResp struct {
		Content      string `json:"content"`
		FinishReason string `json:"finish_reason"`
		Error        string `json:"error"`
	}
	if err := json.Unmarshal(respBody, &dispatchResp); err != nil {
		return &LastResult{
			Status:    "success",
			ToolCalls: 0,
		}, nil
	}

	if dispatchResp.FinishReason == "error" || dispatchResp.Error != "" {
		return &LastResult{
			Status: "failure",
			Error:  dispatchResp.Error,
		}, nil
	}

	findings := extractFindings(dispatchResp.Content, c.ID, runID)

	return &LastResult{
		Status:    "success",
		ToolCalls: 0, // TinyClaw doesn't report individual tool calls in dispatch response
		Findings:  findings,
	}, nil
}
