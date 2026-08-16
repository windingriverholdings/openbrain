// Package converse provides bounded, read-only conversations over retrieved notes.
//
// # Design notes for local models
//
// This package targets small local models served by Ollama on
// memory-constrained hosts, which drives three non-obvious choices.
//
// First, every request sends an explicit options.num_ctx. Ollama otherwise
// applies the model default (often 4096) and silently truncates the prompt.
// Truncation removes the *leading* notes, which are exactly the ones the
// answer's [n] citation markers refer to, so an over-long prompt does not
// merely lose context, it produces confidently mis-numbered citations.
//
// Second, every request sends keep_alive. A conversation is two generations
// separated by embedding and SQL work; without keep_alive the model can be
// evicted in between and reloaded at a cost of 7-18 s, which is the dominant
// term in end-to-end latency on a host that cannot hold the generation model
// and the embedding model simultaneously.
//
// Third, the notes block is budgeted in aggregate rather than per note.
// Prompt ingestion cost grows with prompt size and becomes the largest single
// cost well before the context window is full: a 28k-token notes block was
// measured at 71 s of prompt evaluation alone on a 4B model.
package converse

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/windingriverholdings/openbrain/internal/config"
	"github.com/windingriverholdings/openbrain/internal/model"
)

const systemPrompt = "You answer questions using only the supplied OpenBrain notes. Do not invent facts. Say when the notes are insufficient and mention conflicts."

// Defaults applied when the corresponding config value is unset or invalid.
const (
	defaultNumCtx         = 8192
	defaultPromptTokens   = 3000
	defaultKeepAlive      = 30 * time.Minute
	defaultRewriteTimeout = 25 * time.Second
	defaultAnswerTimeout  = 90 * time.Second
	defaultBaseURL        = "http://localhost:11434"

	// rewriteMaxTokens bounds the retrieval-query rewrite. A rewrite is a
	// handful of keywords; without a cap, models emit paragraphs of reasoning.
	rewriteMaxTokens = 48
	// gateMaxTokens bounds the optional sufficiency gate to one word.
	gateMaxTokens = 4
	// answerMaxTokens bounds the final answer so a runaway generation cannot
	// consume the entire answer timeout.
	answerMaxTokens = 800

	// maxRewriteRunes rejects a "rewrite" that is really an answer.
	maxRewriteRunes = 240

	// scannerMaxBytes raises bufio.Scanner's 64 KB default token limit. A
	// non-streaming Ollama response is a single line and can exceed 64 KB,
	// which would otherwise surface as an opaque bufio.ErrTooLong.
	scannerMaxBytes = 8 << 20 // 8 MB

	// runesPerToken is a deliberately conservative characters-per-token
	// estimate used for budgeting. Under-estimating tokens is the dangerous
	// direction (it overflows the context), so this errs low.
	runesPerToken = 3.5
)

// Provider answers questions grounded in retrieved notes.
//
// NeedsMore is intentionally absent. It previously gated additional retrieval
// rounds, but it was the single most expensive call in the loop while
// providing no usable signal: small models answered "MORE" even when given a
// trivially sufficient context, and the fail-open default meant an
// unrecognized reply also continued. Retrieval breadth is now decided
// deterministically by the caller. Callers that still want a gate can use
// SufficiencyGate, which fails closed.
type Provider interface {
	UnderstandQuery(ctx context.Context, question string) (string, error)
	StreamAnswer(ctx context.Context, question, searchQuery string, notes []model.ThoughtRow, onChunk func(string) error) (string, error)
}

// Gater optionally decides whether more retrieval is warranted. Implemented by
// the Ollama provider but only consulted when the caller allows >1 round.
type Gater interface {
	SufficiencyGate(ctx context.Context, question, searchQuery string, notes []model.ThoughtRow) (enough bool, err error)
}

type ollamaProvider struct {
	model        string
	rewriteModel string
	baseURL      string
	client       *http.Client

	numCtx         int
	promptTokens   int
	keepAlive      time.Duration
	rewriteTimeout time.Duration
	answerTimeout  time.Duration
}

// New builds a conversational provider from configuration.
//
// ConverseModel wins over the older SearchSummaryModel / SearchAssistedModel /
// ExtractModel chain so that conversation model choice is independent of
// extraction model choice; the two have very different size/latency tradeoffs.
func New(cfg *config.Config) (Provider, error) {
	if cfg == nil {
		return nil, fmt.Errorf("configuration is unavailable")
	}
	modelName := firstNonEmpty(
		cfg.ConverseModel,
		cfg.SearchSummaryModel,
		cfg.SearchAssistedModel,
		cfg.ExtractModelFast,
		cfg.ExtractModel,
	)
	if modelName == "" {
		return nil, fmt.Errorf("no local model configured for conversations: set OPENBRAIN_CONVERSE_MODEL")
	}

	p := &ollamaProvider{
		model:          modelName,
		rewriteModel:   firstNonEmpty(cfg.ConverseRewriteModel, modelName),
		baseURL:        strings.TrimSuffix(firstNonEmpty(cfg.OllamaBaseURL, defaultBaseURL), "/"),
		numCtx:         positiveOr(cfg.ConverseNumCtx, defaultNumCtx),
		promptTokens:   positiveOr(cfg.ConversePromptTokens, defaultPromptTokens),
		keepAlive:      positiveDurationOr(cfg.ConverseKeepAlive, defaultKeepAlive),
		rewriteTimeout: positiveDurationOr(cfg.ConverseRewriteTimeout, defaultRewriteTimeout),
		answerTimeout:  positiveDurationOr(cfg.ConverseAnswerTimeout, defaultAnswerTimeout),
	}
	// A dedicated client, because http.DefaultClient has no timeout at all: a
	// wedged Ollama would hang until the caller's context expired, reported as
	// an opaque deadline error rather than a connection problem.
	p.client = &http.Client{
		Transport: &http.Transport{
			ResponseHeaderTimeout: 60 * time.Second,
			IdleConnTimeout:       90 * time.Second,
		},
	}
	return p, nil
}

// Preflight verifies the configured model is actually available, converting a
// mid-query failure into an actionable startup error. A missing model is a
// common misconfiguration (for example naming an "-mlx" tag that was never
// pulled) and is otherwise reported only as a generic 404 at query time.
func (p *ollamaProvider) Preflight(ctx context.Context) error {
	for _, name := range dedupe([]string{p.model, p.rewriteModel}) {
		body, err := json.Marshal(map[string]string{"model": name})
		if err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/api/show", bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := p.client.Do(req)
		if err != nil {
			return fmt.Errorf("ollama unreachable at %s: %w", p.baseURL, err)
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			return fmt.Errorf("model %q is not available in ollama (pull it or fix OPENBRAIN_CONVERSE_MODEL): %s", name, strings.TrimSpace(string(raw)))
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("ollama /api/show returned %d for model %q: %s", resp.StatusCode, name, strings.TrimSpace(string(raw)))
		}
	}
	return nil
}

// Warm issues a minimal generation so the first real question does not pay the
// model's cold-load cost. Errors are advisory; callers may ignore them.
func (p *ollamaProvider) Warm(ctx context.Context) error {
	_, err := p.generate(ctx, generateRequest{
		step:      "warm",
		model:     p.model,
		prompt:    "ok",
		maxTokens: 1,
		timeout:   p.rewriteTimeout,
	})
	return err
}

// UnderstandQuery converts a conversational question into a retrieval query.
//
// A failure here is recoverable by the caller (the raw question is a usable
// query), so the error is returned rather than being treated as fatal.
func (p *ollamaProvider) UnderstandQuery(ctx context.Context, question string) (string, error) {
	prompt := "User question:\n" + strings.TrimSpace(question) +
		"\n\nReturn only a short OpenBrain retrieval query. Keep names, acronyms, subjects, and key concepts. " +
		"Remove conversational filler such as 'what is', 'can you', or 'tell me about'. Do not answer the question."

	text, err := p.generate(ctx, generateRequest{
		step:      "rewrite",
		model:     p.rewriteModel,
		prompt:    prompt,
		maxTokens: rewriteMaxTokens,
		timeout:   p.rewriteTimeout,
	})
	if err != nil {
		return "", err
	}
	query := sanitizeRewrite(text)
	if query == "" {
		return "", fmt.Errorf("local query understanding returned an empty search query")
	}
	if len([]rune(query)) > maxRewriteRunes {
		return "", fmt.Errorf("local query understanding returned %d runes, expected a short query (max %d)", len([]rune(query)), maxRewriteRunes)
	}
	return query, nil
}

// SufficiencyGate reports whether the notes already support a complete answer.
//
// Unlike the previous NeedsMore, this fails *closed*: anything other than an
// unambiguous MORE stops retrieval. Small models over-report insufficiency, so
// a fail-open default caused every round to run and burned the time budget.
func (p *ollamaProvider) SufficiencyGate(ctx context.Context, question, searchQuery string, notes []model.ThoughtRow) (bool, error) {
	prompt := "User question:\n" + strings.TrimSpace(question) +
		"\n\nRetrieval focus:\n" + strings.TrimSpace(searchQuery) +
		"\n\nNotes read so far:\n" + FormatNotes(notes, p.promptTokens) +
		"\n\nReply with exactly one word: ENOUGH if these notes support an answer, otherwise MORE."

	text, err := p.generate(ctx, generateRequest{
		step:      "gate",
		model:     p.model,
		prompt:    prompt,
		maxTokens: gateMaxTokens,
		timeout:   p.rewriteTimeout,
	})
	if err != nil {
		return true, err
	}
	// Fail closed: only an explicit MORE requests another round.
	return !strings.HasPrefix(strings.ToUpper(strings.TrimSpace(text)), "MORE"), nil
}

// StreamAnswer produces the grounded answer, streaming chunks as they arrive.
func (p *ollamaProvider) StreamAnswer(ctx context.Context, question, searchQuery string, notes []model.ThoughtRow, onChunk func(string) error) (string, error) {
	prompt := "User question:\n" + strings.TrimSpace(question) +
		"\n\nRetrieval focus:\n" + strings.TrimSpace(searchQuery) +
		"\n\nNotes:\n" + FormatNotes(notes, p.promptTokens) +
		"\n\nAnswer directly using only these notes. Cite supporting notes inline using their bracket number, " +
		"for example [1] or [1, 2]. Only cite numbers that appear above. " +
		"If the notes are insufficient, say so plainly. If they conflict, say so and cite both."

	return p.generate(ctx, generateRequest{
		step:      "answer",
		model:     p.model,
		prompt:    prompt,
		maxTokens: answerMaxTokens,
		timeout:   p.answerTimeout,
		stream:    true,
		onChunk:   onChunk,
	})
}

type generateRequest struct {
	step      string
	model     string
	prompt    string
	maxTokens int
	timeout   time.Duration
	stream    bool
	onChunk   func(string) error
}

// ollamaPayload mirrors the subset of Ollama's /api/generate body we set.
//
// options and keep_alive were previously omitted entirely, which left
// generation length, context size, and residency to server defaults. think is
// disabled because reasoning tokens are pure latency here: they are not shown
// to the user and measurably doubled call duration.
type ollamaPayload struct {
	Model     string         `json:"model"`
	Prompt    string         `json:"prompt"`
	System    string         `json:"system"`
	Stream    bool           `json:"stream"`
	KeepAlive string         `json:"keep_alive,omitempty"`
	Think     *bool          `json:"think,omitempty"`
	Options   map[string]any `json:"options,omitempty"`
}

// ollamaResponse covers both streaming chunks and the single non-streaming
// object. The *_duration fields are nanosecond timings Ollama always returns;
// they are logged because they are the only way to attribute latency between
// model loading, prompt ingestion, and generation.
type ollamaResponse struct {
	Response           string `json:"response"`
	Done               bool   `json:"done"`
	DoneReason         string `json:"done_reason"`
	Error              string `json:"error"`
	LoadDuration       int64  `json:"load_duration"`
	PromptEvalCount    int    `json:"prompt_eval_count"`
	PromptEvalDuration int64  `json:"prompt_eval_duration"`
	EvalCount          int    `json:"eval_count"`
	EvalDuration       int64  `json:"eval_duration"`
}

func (p *ollamaProvider) generate(ctx context.Context, gr generateRequest) (string, error) {
	// Per-call deadline. A single deadline spanning the whole conversation let
	// a slow rewrite consume the answer's budget, so each call is bounded
	// independently and still respects any shorter caller deadline.
	if gr.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, gr.timeout)
		defer cancel()
	}

	think := false
	payload := ollamaPayload{
		Model:     gr.model,
		Prompt:    gr.prompt,
		System:    systemPrompt,
		Stream:    gr.stream,
		KeepAlive: durationToOllama(p.keepAlive),
		Think:     &think,
		Options: map[string]any{
			"num_ctx":     p.numCtx,
			"num_predict": gr.maxTokens,
			"temperature": 0.2,
		},
	}

	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(payload); err != nil {
		return "", err
	}

	promptRunes := len([]rune(gr.prompt))
	started := time.Now()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/api/generate", &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return "", p.describeError(gr, promptRunes, started, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return "", fmt.Errorf("conversational %s step: ollama returned status %d for model %q: %s",
			gr.step, resp.StatusCode, gr.model, strings.TrimSpace(string(raw)))
	}

	var out strings.Builder
	var last ollamaResponse
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64<<10), scannerMaxBytes)

	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var chunk ollamaResponse
		if err := json.Unmarshal(line, &chunk); err != nil {
			return "", fmt.Errorf("conversational %s step: decode ollama response: %w", gr.step, err)
		}
		if chunk.Error != "" {
			return "", fmt.Errorf("conversational %s step: ollama error: %s", gr.step, chunk.Error)
		}
		out.WriteString(chunk.Response)
		if gr.onChunk != nil && chunk.Response != "" {
			if err := gr.onChunk(chunk.Response); err != nil {
				return "", err
			}
		}
		if chunk.Done {
			last = chunk
			break
		}
		last = chunk
	}
	if err := scanner.Err(); err != nil {
		return "", p.describeError(gr, promptRunes, started, err)
	}

	// Attribute latency across load / prompt ingest / generation. Without
	// this breakdown a slow conversation is indistinguishable from a hung one.
	slog.Debug("conversational llm call",
		"step", gr.step,
		"model", gr.model,
		"wall_ms", time.Since(started).Milliseconds(),
		"prompt_runes", promptRunes,
		"load_ms", last.LoadDuration/1e6,
		"prompt_tokens", last.PromptEvalCount,
		"prompt_eval_ms", last.PromptEvalDuration/1e6,
		"gen_tokens", last.EvalCount,
		"gen_eval_ms", last.EvalDuration/1e6,
		"done_reason", last.DoneReason,
	)

	return strings.TrimSpace(out.String()), nil
}

// describeError turns Go's generic transport failures into messages that name
// the step, model, elapsed time, and prompt size. The bare
// `context deadline exceeded` this replaces gave no way to tell a cold model
// load from an oversized prompt from an unreachable server.
func (p *ollamaProvider) describeError(gr generateRequest, promptRunes int, started time.Time, err error) error {
	elapsed := time.Since(started).Round(time.Millisecond)

	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf(
			"conversational %s step timed out after %s (model %q, ~%d prompt tokens, limit %s): "+
				"the model may be loading or the prompt may be too large; "+
				"try a smaller OPENBRAIN_CONVERSE_MODEL, lower OPENBRAIN_CONVERSE_PROMPT_TOKENS, "+
				"or raise OPENBRAIN_CONVERSE_%s_TIMEOUT: %w",
			gr.step, elapsed, gr.model, estimateTokens(promptRunes), gr.timeout,
			strings.ToUpper(timeoutKnob(gr.step)), err)
	}
	if errors.Is(err, bufio.ErrTooLong) {
		return fmt.Errorf("conversational %s step: ollama response line exceeded %d bytes: %w", gr.step, scannerMaxBytes, err)
	}
	return fmt.Errorf("conversational %s step failed after %s (model %q, ~%d prompt tokens): %w",
		gr.step, elapsed, gr.model, estimateTokens(promptRunes), err)
}

func timeoutKnob(step string) string {
	if step == "answer" {
		return "answer"
	}
	return "rewrite"
}

// --- helpers ---------------------------------------------------------------

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

func positiveOr(v, fallback int) int {
	if v > 0 {
		return v
	}
	return fallback
}

func positiveDurationOr(v, fallback time.Duration) time.Duration {
	if v > 0 {
		return v
	}
	return fallback
}

func dedupe(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// durationToOllama renders a duration in the string form Ollama accepts.
func durationToOllama(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	return d.String()
}

func estimateTokens(runes int) int {
	return int(float64(runes) / runesPerToken)
}

// sanitizeRewrite strips the decorations small models add around a bare query:
// code fences, quotes, a leading label, and any explanation after a newline.
func sanitizeRewrite(text string) string {
	s := strings.TrimSpace(text)
	s = strings.TrimPrefix(s, "```")
	if i := strings.Index(s, "```"); i >= 0 {
		s = s[:i]
	}
	// Keep only the first non-empty line; models often append a rationale.
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		s = line
		break
	}
	// Drop a leading "Query:" / "Search query:" style label.
	for _, label := range []string{"retrieval query:", "search query:", "query:", "search:"} {
		if len(s) >= len(label) && strings.EqualFold(s[:len(label)], label) {
			s = strings.TrimSpace(s[len(label):])
			break
		}
	}
	return strings.TrimSpace(strings.Trim(s, "`\"' \t\r\n"))
}
