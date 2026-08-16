package converse

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/windingriverholdings/openbrain/internal/config"
	"github.com/windingriverholdings/openbrain/internal/model"
)

func testProvider(t *testing.T, baseURL string) *ollamaProvider {
	t.Helper()
	p, err := New(&config.Config{
		ConverseModel:          "test-model",
		OllamaBaseURL:          baseURL,
		ConverseNumCtx:         4096,
		ConversePromptTokens:   500,
		ConverseKeepAlive:      time.Minute,
		ConverseRewriteTimeout: 2 * time.Second,
		ConverseAnswerTimeout:  2 * time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p.(*ollamaProvider)
}

func TestNew_PrefersConverseModelOverExtractModel(t *testing.T) {
	p, err := New(&config.Config{ConverseModel: "converse-m", ExtractModel: "extract-m"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := p.(*ollamaProvider).model; got != "converse-m" {
		t.Fatalf("model = %q, want converse-m", got)
	}
}

func TestNew_FallsBackThroughChain(t *testing.T) {
	p, err := New(&config.Config{ExtractModel: "extract-m"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := p.(*ollamaProvider).model; got != "extract-m" {
		t.Fatalf("model = %q, want extract-m", got)
	}
}

func TestNew_NoModelConfigured(t *testing.T) {
	if _, err := New(&config.Config{}); err == nil {
		t.Fatal("expected error when no model is configured")
	}
}

// TestGenerate_SendsBoundingOptions guards the settings that make local models
// usable: an explicit context size (so Ollama cannot silently truncate the
// prompt and corrupt citation numbering), a generation cap, keep_alive (so the
// model is not evicted between the rewrite and answer calls), and think=false.
func TestGenerate_SendsBoundingOptions(t *testing.T) {
	var got ollamaPayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode request: %v", err)
		}
		json.NewEncoder(w).Encode(ollamaResponse{Response: "search terms", Done: true})
	}))
	defer srv.Close()

	p := testProvider(t, srv.URL)
	if _, err := p.UnderstandQuery(context.Background(), "what did I decide?"); err != nil {
		t.Fatalf("UnderstandQuery: %v", err)
	}

	if got.Options["num_ctx"] != float64(4096) {
		t.Errorf("num_ctx = %v, want 4096", got.Options["num_ctx"])
	}
	if got.Options["num_predict"] != float64(rewriteMaxTokens) {
		t.Errorf("num_predict = %v, want %d", got.Options["num_predict"], rewriteMaxTokens)
	}
	if got.KeepAlive == "" {
		t.Error("keep_alive not sent; model may be evicted between calls")
	}
	if got.Think == nil || *got.Think {
		t.Error("think should be explicitly false to avoid reasoning-token latency")
	}
}

func TestUnderstandQuery_StripsModelDecoration(t *testing.T) {
	cases := []struct{ raw, want string }{
		{"search timeout decision", "search timeout decision"},
		{"```\nsearch timeout\n```", "search timeout"},
		{"\"search timeout\"", "search timeout"},
		{"Query: search timeout", "search timeout"},
		{"Search query: agent timeout", "agent timeout"},
		{"agent timeout\nThis removes filler words.", "agent timeout"},
	}
	for _, tc := range cases {
		raw := tc.raw
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(ollamaResponse{Response: raw, Done: true})
		}))
		p := testProvider(t, srv.URL)
		got, err := p.UnderstandQuery(context.Background(), "q")
		srv.Close()
		if err != nil {
			t.Fatalf("UnderstandQuery(%q): %v", raw, err)
		}
		if got != tc.want {
			t.Errorf("UnderstandQuery(%q) = %q, want %q", raw, got, tc.want)
		}
	}
}

func TestUnderstandQuery_RejectsAnswerShapedReply(t *testing.T) {
	long := strings.Repeat("this is an explanation not a query ", 20)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(ollamaResponse{Response: long, Done: true})
	}))
	defer srv.Close()
	if _, err := testProvider(t, srv.URL).UnderstandQuery(context.Background(), "q"); err == nil {
		t.Fatal("expected rejection of an over-long rewrite")
	}
}

func TestStreamAnswer_StreamsChunks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher := w.(http.Flusher)
		for _, chunk := range []string{"You ", "decided ", "X [1]."} {
			json.NewEncoder(w).Encode(ollamaResponse{Response: chunk})
			flusher.Flush()
		}
		json.NewEncoder(w).Encode(ollamaResponse{Done: true, DoneReason: "stop"})
	}))
	defer srv.Close()

	var chunks []string
	full, err := testProvider(t, srv.URL).StreamAnswer(context.Background(), "q", "sq",
		[]model.ThoughtRow{{ID: "a", Content: "note"}},
		func(c string) error { chunks = append(chunks, c); return nil })
	if err != nil {
		t.Fatalf("StreamAnswer: %v", err)
	}
	if full != "You decided X [1]." {
		t.Errorf("answer = %q", full)
	}
	if len(chunks) != 3 {
		t.Errorf("got %d chunks, want 3", len(chunks))
	}
}

// TestGenerate_HandlesOversizedLine covers the previous 64 KB bufio.Scanner
// default, which turned any large non-streaming response into an opaque
// bufio.ErrTooLong.
func TestGenerate_HandlesOversizedLine(t *testing.T) {
	big := strings.Repeat("x", 200_000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(ollamaResponse{Response: big, Done: true})
	}))
	defer srv.Close()

	got, err := testProvider(t, srv.URL).StreamAnswer(context.Background(), "q", "sq",
		[]model.ThoughtRow{{ID: "a", Content: "n"}}, nil)
	if err != nil {
		t.Fatalf("StreamAnswer with %d-byte line: %v", len(big), err)
	}
	if len(got) != len(big) {
		t.Errorf("got %d bytes, want %d", len(got), len(big))
	}
}

func TestGenerate_ErrorMessageIsDiagnostic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"error":"model not found"}`)
	}))
	defer srv.Close()

	_, err := testProvider(t, srv.URL).UnderstandQuery(context.Background(), "q")
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{"rewrite", "404", "test-model"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

func TestGenerate_TimeoutNamesStepAndRemedy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
	}))
	defer srv.Close()

	p := testProvider(t, srv.URL)
	p.rewriteTimeout = 50 * time.Millisecond
	_, err := p.UnderstandQuery(context.Background(), "q")
	if err == nil {
		t.Fatal("expected timeout")
	}
	for _, want := range []string{"rewrite", "timed out", "OPENBRAIN_CONVERSE"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

func TestGenerate_PropagatesCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(time.Second)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()

	_, err := testProvider(t, srv.URL).UnderstandQuery(ctx, "q")
	if err != context.Canceled {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestGenerate_SurfacesInlineOllamaError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(ollamaResponse{Error: "out of memory"})
	}))
	defer srv.Close()

	_, err := testProvider(t, srv.URL).UnderstandQuery(context.Background(), "q")
	if err == nil || !strings.Contains(err.Error(), "out of memory") {
		t.Fatalf("err = %v, want inline ollama error", err)
	}
}

func TestSufficiencyGate_FailsClosed(t *testing.T) {
	// Anything other than an explicit MORE must stop retrieval. The previous
	// fail-open behaviour meant garbled replies always requested another round.
	cases := map[string]bool{
		"MORE":                 false,
		"more":                 false,
		"ENOUGH":               true,
		"I think that is fine": true,
		"":                     true,
	}
	for reply, wantEnough := range cases {
		r := reply
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			json.NewEncoder(w).Encode(ollamaResponse{Response: r, Done: true})
		}))
		enough, err := testProvider(t, srv.URL).SufficiencyGate(context.Background(), "q", "sq",
			[]model.ThoughtRow{{ID: "a", Content: "n"}})
		srv.Close()
		if err != nil {
			t.Fatalf("SufficiencyGate(%q): %v", r, err)
		}
		if enough != wantEnough {
			t.Errorf("SufficiencyGate(%q) enough = %v, want %v", r, enough, wantEnough)
		}
	}
}

func TestPreflight_ReportsMissingModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"error":"model 'test-model' not found"}`)
	}))
	defer srv.Close()

	err := testProvider(t, srv.URL).Preflight(context.Background())
	if err == nil {
		t.Fatal("expected preflight failure")
	}
	if !strings.Contains(err.Error(), "not available") || !strings.Contains(err.Error(), "test-model") {
		t.Errorf("error %q should name the missing model and be actionable", err)
	}
}

func TestPreflight_AcceptsAvailableModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"details":{}}`)
	}))
	defer srv.Close()
	if err := testProvider(t, srv.URL).Preflight(context.Background()); err != nil {
		t.Fatalf("Preflight: %v", err)
	}
}
