package orchidx

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mr-joshcrane/orchid"
)

func testHctx() orchid.HandlerContext {
	var events []orchid.Event
	return orchid.HandlerContext{
		Ctx:       context.Background(),
		ProcessID: "test_proc",
		ToolName:  "test_tool",
		LogAppend: func(e orchid.Event) { events = append(events, e) },
	}
}

func testHctxWithLog(events *[]orchid.Event) orchid.HandlerContext {
	return orchid.HandlerContext{
		Ctx:       context.Background(),
		ProcessID: "test_proc",
		ToolName:  "test_tool",
		LogAppend: func(e orchid.Event) { *events = append(*events, e) },
	}
}

func TestCapOutput(t *testing.T) {
	tests := []struct {
		name          string
		maxBytes      int
		input         string
		handlerErr    error
		wantTruncated bool
		wantMarker    bool
		wantErr       bool
	}{
		{
			name:          "under limit unchanged",
			maxBytes:      100,
			input:         "short",
			wantTruncated: false,
		},
		{
			name:          "at limit unchanged",
			maxBytes:      5,
			input:         "12345",
			wantTruncated: false,
		},
		{
			name:          "over limit truncated with marker",
			maxBytes:      5,
			input:         "1234567890",
			wantTruncated: true,
			wantMarker:    true,
		},
		{
			name:       "handler error passes through",
			maxBytes:   100,
			input:      "data",
			handlerErr: errors.New("boom"),
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := func(_ orchid.HandlerContext, _ map[string]string) (string, error) {
				return tt.input, tt.handlerErr
			}
			var events []orchid.Event
			hctx := testHctxWithLog(&events)

			mw := CapOutput(tt.maxBytes)
			wrapped := mw(base)
			result, err := wrapped(hctx, nil)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.wantTruncated {
				if !strings.Contains(result, "[orchid:truncated") {
					t.Fatalf("expected truncation marker, got: %s", result)
				}
				want := fmt.Sprintf("[orchid:truncated bytes_total=%d bytes_shown=%d]", len(tt.input), tt.maxBytes)
				if !strings.Contains(result, want) {
					t.Fatalf("marker mismatch, got: %s", result)
				}
				if len(events) != 1 {
					t.Fatalf("expected 1 EventOutputTruncated event, got %d", len(events))
				}
				if events[0].Type != orchid.EventOutputTruncated {
					t.Fatalf("event type = %v, want EventOutputTruncated", events[0].Type)
				}
				return
			}

			if result != tt.input {
				t.Fatalf("result = %q, want %q", result, tt.input)
			}
			if len(events) != 0 {
				t.Fatalf("expected no events for non-truncated output, got %d", len(events))
			}
		})
	}
}

func TestTimeout(t *testing.T) {
	tests := []struct {
		name      string
		timeout   time.Duration
		sleep     time.Duration
		wantErr   bool
		errSubstr string
	}{
		{
			name:    "completes within timeout",
			timeout: 500 * time.Millisecond,
			sleep:   0,
			wantErr: false,
		},
		{
			name:      "exceeds timeout",
			timeout:   10 * time.Millisecond,
			sleep:     200 * time.Millisecond,
			wantErr:   true,
			errSubstr: "TOOL_TIMEOUT",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := func(hctx orchid.HandlerContext, _ map[string]string) (string, error) {
				select {
				case <-time.After(tt.sleep):
					return "ok", nil
				case <-hctx.Ctx.Done():
					return "", hctx.Ctx.Err()
				}
			}
			hctx := testHctx()
			mw := Timeout(tt.timeout)
			wrapped := mw(base)
			result, err := wrapped(hctx, nil)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected timeout error")
				}
				if !strings.Contains(err.Error(), tt.errSubstr) {
					t.Fatalf("error = %q, want containing %q", err.Error(), tt.errSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result != "ok" {
				t.Fatalf("result = %q, want %q", result, "ok")
			}
		})
	}
}

func TestRedact(t *testing.T) {
	awsKey := regexp.MustCompile(`AKIA[0-9A-Z]{16}`)
	bearer := regexp.MustCompile(`Bearer\s+[A-Za-z0-9\-._~+/]+=*`)

	tests := []struct {
		name     string
		patterns []*regexp.Regexp
		input    string
		want     string
		err      error
	}{
		{
			name:     "no patterns no change",
			patterns: nil,
			input:    "hello world",
			want:     "hello world",
		},
		{
			name:     "aws key redacted",
			patterns: []*regexp.Regexp{awsKey},
			input:    "key=AKIAIOSFODNN7EXAMPLE end",
			want:     "key=[REDACTED] end",
		},
		{
			name:     "bearer token redacted",
			patterns: []*regexp.Regexp{bearer},
			input:    "Authorization: Bearer eyJhbGciOiJIUzI1NiJ9 done",
			want:     "Authorization: [REDACTED] done",
		},
		{
			name:     "multiple patterns",
			patterns: []*regexp.Regexp{awsKey, bearer},
			input:    "AKIAIOSFODNN7EXAMPLE Bearer abc123",
			want:     "[REDACTED] [REDACTED]",
		},
		{
			name:     "error passes through",
			patterns: []*regexp.Regexp{awsKey},
			input:    "AKIAIOSFODNN7EXAMPLE",
			err:      errors.New("fail"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := func(_ orchid.HandlerContext, _ map[string]string) (string, error) {
				return tt.input, tt.err
			}
			hctx := testHctx()
			mw := Redact(tt.patterns...)
			wrapped := mw(base)
			result, err := wrapped(hctx, nil)

			if tt.err != nil {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result != tt.want {
				t.Fatalf("result = %q, want %q", result, tt.want)
			}
		})
	}
}

type testSink struct {
	calls []sinkCall
}

type sinkCall struct {
	tool    string
	durMS   int64
	bytesIn int
	bytesOut int
	err     error
}

func (s *testSink) Observe(tool string, durMS int64, bytesIn int, bytesOut int, err error) {
	s.calls = append(s.calls, sinkCall{tool, durMS, bytesIn, bytesOut, err})
}

func TestMeter(t *testing.T) {
	tests := []struct {
		name       string
		args       map[string]string
		output     string
		handlerErr error
		wantErr    bool
	}{
		{
			name:   "success records metrics",
			args:   map[string]string{"path": "/tmp/test"},
			output: "file-content",
		},
		{
			name:       "error still records metrics",
			args:       map[string]string{"x": "y"},
			handlerErr: errors.New("boom"),
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sink := &testSink{}
			base := func(_ orchid.HandlerContext, _ map[string]string) (string, error) {
				return tt.output, tt.handlerErr
			}
			hctx := testHctx()
			mw := Meter(sink)
			wrapped := mw(base)
			result, err := wrapped(hctx, tt.args)

			if len(sink.calls) != 1 {
				t.Fatalf("expected 1 sink call, got %d", len(sink.calls))
			}
			call := sink.calls[0]
			if call.tool != "test_tool" {
				t.Fatalf("tool = %q, want %q", call.tool, "test_tool")
			}
			if call.durMS < 0 {
				t.Fatalf("duration should be non-negative, got %d", call.durMS)
			}

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				if call.err == nil {
					t.Fatal("sink should record the error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result != tt.output {
				t.Fatalf("result = %q, want %q", result, tt.output)
			}
			if call.bytesOut != len(tt.output) {
				t.Fatalf("bytesOut = %d, want %d", call.bytesOut, len(tt.output))
			}
		})
	}
}
