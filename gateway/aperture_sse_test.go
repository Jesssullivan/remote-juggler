package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestApertureSSEIngester_Configured(t *testing.T) {
	t.Run("empty URL", func(t *testing.T) {
		s := NewApertureSSEIngester("", nil, nil)
		if s.Configured() {
			t.Error("expected not configured with empty URL")
		}
	})
	t.Run("with URL", func(t *testing.T) {
		s := NewApertureSSEIngester("http://ai", nil, nil)
		if !s.Configured() {
			t.Error("expected configured with URL")
		}
	})
}

func queryMeter(meter *MeterStore, agent string) *MeterBucket {
	buckets := meter.Query(agent, "")
	if len(buckets) == 0 {
		return &MeterBucket{}
	}
	// Sum all buckets for this agent.
	result := &MeterBucket{}
	for _, b := range buckets {
		result.ToolCalls += b.ToolCalls
		result.InputTokens += b.InputTokens
		result.OutputTokens += b.OutputTokens
		result.ErrorCount += b.ErrorCount
	}
	return result
}

func TestApertureSSEIngester_ProcessMetric(t *testing.T) {
	meter := NewMeterStore()
	s := NewApertureSSEIngester("http://ai", meter, nil)

	data := []byte(`{"id":1,"timestamp":"2026-02-26T14:00:00Z","model":"claude-haiku-4-5-20251001","input_tokens":100,"output_tokens":50,"duration_ms":500,"status_code":200,"login_name":"test@user"}`)

	if err := s.processMetric(data); err != nil {
		t.Fatalf("processMetric error: %v", err)
	}

	summary := queryMeter(meter, "test@user")
	if summary.InputTokens != 100 {
		t.Errorf("expected 100 input tokens, got %d", summary.InputTokens)
	}
	if summary.OutputTokens != 50 {
		t.Errorf("expected 50 output tokens, got %d", summary.OutputTokens)
	}
}

func TestApertureSSEIngester_ProcessMetricError(t *testing.T) {
	meter := NewMeterStore()
	s := NewApertureSSEIngester("http://ai", meter, nil)

	data := []byte(`{"id":2,"timestamp":"2026-02-26T14:00:00Z","model":"claude-haiku-4-5-20251001","input_tokens":10,"output_tokens":0,"duration_ms":100,"status_code":500,"login_name":"test@user"}`)

	if err := s.processMetric(data); err != nil {
		t.Fatalf("processMetric error: %v", err)
	}

	summary := queryMeter(meter, "test@user")
	if summary.ErrorCount != 1 {
		t.Errorf("expected 1 error, got %d", summary.ErrorCount)
	}
}

func TestApertureSSEIngester_Connect(t *testing.T) {
	meter := NewMeterStore()

	// Create a test SSE server that sends 3 metric events then closes.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/events" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "no flusher", 500)
			return
		}

		// Send a whois event (should be ignored).
		fmt.Fprintf(w, "event: whois\ndata: {\"login_name\":\"test\"}\n\n")
		flusher.Flush()

		// Send 3 metric events.
		for i := 0; i < 3; i++ {
			fmt.Fprintf(w, "event: metric\ndata: {\"id\":%d,\"timestamp\":\"2026-02-26T14:00:00Z\",\"model\":\"claude-haiku-4-5-20251001\",\"input_tokens\":%d,\"output_tokens\":%d,\"duration_ms\":100,\"status_code\":200,\"login_name\":\"agent@test\"}\n\n",
				i+1, (i+1)*10, (i+1)*5)
			flusher.Flush()
		}
		// Close connection (stream ends).
	}))
	defer server.Close()

	s := NewApertureSSEIngester(server.URL, meter, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// connect() should process events then return when stream closes.
	err := s.connect(ctx)
	if err == nil {
		t.Fatal("expected error when stream closes")
	}

	summary := queryMeter(meter, "agent@test")
	if summary.ToolCalls != 3 {
		t.Errorf("expected 3 calls, got %d", summary.ToolCalls)
	}
	// 10+20+30 = 60
	if summary.InputTokens != 60 {
		t.Errorf("expected 60 input tokens, got %d", summary.InputTokens)
	}
	// 5+10+15 = 30
	if summary.OutputTokens != 30 {
		t.Errorf("expected 30 output tokens, got %d", summary.OutputTokens)
	}
}

func TestDeadlineReaderTimeout(t *testing.T) {
	// Create a reader that blocks forever.
	blockForever := &blockingReader{}
	dr := newDeadlineReader(blockForever, 200*time.Millisecond)

	buf := make([]byte, 64)
	start := time.Now()
	_, err := dr.Read(buf)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Errorf("expected timeout error, got: %v", err)
	}
	if elapsed < 150*time.Millisecond || elapsed > 1*time.Second {
		t.Errorf("expected ~200ms timeout, got %v", elapsed)
	}
}

func TestDeadlineReaderPassthrough(t *testing.T) {
	// Normal reader should pass through without timeout.
	r := strings.NewReader("hello world")
	dr := newDeadlineReader(r, 5*time.Second)

	buf := make([]byte, 64)
	n, err := dr.Read(buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(buf[:n]) != "hello world" {
		t.Errorf("expected 'hello world', got %q", string(buf[:n]))
	}
}

// blockingReader blocks forever on Read.
type blockingReader struct{}

func (b *blockingReader) Read(p []byte) (int, error) {
	select {} // block forever
}

func TestApertureSSEIngester_IgnoresNonMetricEvents(t *testing.T) {
	meter := NewMeterStore()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)

		fmt.Fprintf(w, "event: whois\ndata: {\"login_name\":\"test\"}\n\n")
		flusher.Flush()
		fmt.Fprintf(w, "event: config\ndata: {\"updated\":true}\n\n")
		flusher.Flush()
		fmt.Fprintf(w, "event: metric\ndata: {\"id\":1,\"timestamp\":\"2026-02-26T14:00:00Z\",\"model\":\"claude-haiku-4-5-20251001\",\"input_tokens\":10,\"output_tokens\":5,\"duration_ms\":100,\"status_code\":200,\"login_name\":\"agent@test\"}\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	s := NewApertureSSEIngester(server.URL, meter, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	s.connect(ctx)

	summary := queryMeter(meter, "agent@test")
	if summary.ToolCalls != 1 {
		t.Errorf("expected 1 call (only metric events), got %d", summary.ToolCalls)
	}
}
