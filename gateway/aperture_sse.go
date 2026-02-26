package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

// ApertureSSEIngester connects to Aperture's /api/events SSE stream
// and feeds metric events into the MeterStore. This provides real-time
// LLM usage data without relying on HTTP webhooks (which Aperture
// accepts in config but does not currently fire).
type ApertureSSEIngester struct {
	apertureURL       string
	meter             *MeterStore
	client            *http.Client
	reconnectInterval time.Duration
}

// NewApertureSSEIngester creates an SSE ingester for Aperture events.
func NewApertureSSEIngester(apertureURL string, meter *MeterStore, client *http.Client) *ApertureSSEIngester {
	if client == nil {
		client = &http.Client{
			// No timeout — SSE is a long-lived connection.
			Timeout: 0,
		}
	}
	return &ApertureSSEIngester{
		apertureURL:       strings.TrimRight(apertureURL, "/"),
		meter:             meter,
		client:            client,
		reconnectInterval: 5 * time.Second,
	}
}

// Configured returns whether the SSE ingester has a valid Aperture URL.
func (s *ApertureSSEIngester) Configured() bool {
	return s.apertureURL != ""
}

// apertureMetricEvent is the shape of a metric event from Aperture's SSE stream.
type apertureMetricEvent struct {
	ID           int       `json:"id"`
	CaptureID    string    `json:"capture_id"`
	SessionID    string    `json:"session_id"`
	Timestamp    time.Time `json:"timestamp"`
	Model        string    `json:"model"`
	CachedTokens int       `json:"cached_tokens"`
	InputTokens  int       `json:"input_tokens"`
	OutputTokens int       `json:"output_tokens"`
	ReasonTokens int       `json:"reasoning_tokens"`
	ToolUseCount int       `json:"tool_use_count"`
	DurationMs   int64     `json:"duration_ms"`
	StatusCode   int       `json:"status_code"`
	LoginName    string    `json:"login_name"`
	StableNodeID string    `json:"stable_node_id"`
	UserAgent    string    `json:"user_agent"`
	ToolNames    []string  `json:"tool_names"`
}

// Start begins the SSE connection loop. Returns a cancel function.
func (s *ApertureSSEIngester) Start(ctx context.Context) context.CancelFunc {
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		if !s.Configured() {
			log.Printf("aperture-sse: not configured, skipping")
			return
		}
		log.Printf("aperture-sse: connecting to %s/api/events", s.apertureURL)

		for {
			err := s.connect(ctx)
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				log.Printf("aperture-sse: connection error: %v, reconnecting in %s", err, s.reconnectInterval)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(s.reconnectInterval):
			}
		}
	}()
	return cancel
}

// connect establishes an SSE connection and processes events until disconnected.
func (s *ApertureSSEIngester) connect(ctx context.Context) error {
	url := s.apertureURL + "/api/events"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("aperture returned %d", resp.StatusCode)
	}

	log.Printf("aperture-sse: connected to %s", url)
	ingested := 0

	scanner := bufio.NewScanner(resp.Body)
	var eventType string
	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimPrefix(line, "event: ")
			continue
		}

		if strings.HasPrefix(line, "data: ") && eventType == "metric" {
			data := strings.TrimPrefix(line, "data: ")
			if err := s.processMetric([]byte(data)); err != nil {
				log.Printf("aperture-sse: parse metric: %v", err)
			} else {
				ingested++
				if ingested%10 == 0 {
					log.Printf("aperture-sse: ingested %d metrics", ingested)
				}
			}
			eventType = ""
			continue
		}

		// Empty line resets event type (SSE spec).
		if line == "" {
			eventType = ""
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read stream: %w", err)
	}

	return fmt.Errorf("stream closed by server")
}

// processMetric parses and records a single metric event.
func (s *ApertureSSEIngester) processMetric(data []byte) error {
	var m apertureMetricEvent
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}

	// Derive agent identity from login_name.
	// Aperture uses Tailscale WhoIs for identity, which returns
	// the full tag list for tagged devices or user login for users.
	agent := m.LoginName
	if agent == "" {
		agent = m.StableNodeID
	}

	s.meter.Record(MeterRecord{
		Agent:        agent,
		ToolName:     fmt.Sprintf("llm:%s", m.Model),
		DurationMs:   m.DurationMs,
		Timestamp:    m.Timestamp,
		IsError:      m.StatusCode >= 400,
		InputTokens:  m.InputTokens,
		OutputTokens: m.OutputTokens,
	})

	return nil
}
