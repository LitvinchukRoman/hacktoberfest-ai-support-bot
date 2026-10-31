package llm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"google.golang.org/genai"

	"github.com/handziurdmytro/skelar/internal/support/domain"
)

const (
	DefaultModel         = "gemma-4-26b-a4b-it"
	DefaultFallbackModel = "gemma-4-31b-it"
	DefaultTimeout       = 8 * time.Second

	// fallbackBudget is the least time left for a second attempt to be worth making.
	fallbackBudget = 2 * time.Second
	// primaryShare is the part of the call's time the first model gets when a
	// fallback is configured. The rest is kept for the smaller, faster model.
	primaryShare = 0.6
)

// ErrBadOutput means the model answered, but not with a classification we can read.
var ErrBadOutput = errors.New("model output is not a valid classification")

type Config struct {
	APIKey          string        // GEMINI_API_KEY (Google AI Studio)
	ClassifierModel string        // SUPPORT_CLASSIFIER_MODEL
	WriterModel     string        // SUPPORT_WRITER_MODEL
	FallbackModel   string        // SUPPORT_FALLBACK_MODEL: asked when the first model is overloaded, failing or too slow; "" turns it off
	SendTemperature bool          // SUPPORT_LLM_TEMPERATURE: send temperature 0
	UseSchema       bool          // SUPPORT_LLM_SCHEMA: ask for JSON by MIME type and schema
	ThinkingLevel   string        // SUPPORT_LLM_THINKING: MINIMAL, LOW…; "" sends no thinking config
	Timeout         time.Duration // SUPPORT_LLM_TIMEOUT: one whole call, fallback included (the first model gets 60% of it)
	BaseURL         string        // tests point the client at a local server
}

// Gemini talks to Gemma 4 through the Gemini API. It understands and phrases;
// it never decides. On any error the caller falls back to Fake or Templater.
type Gemini struct {
	client *genai.Client
	cfg    Config
}

func NewGemini(ctx context.Context, cfg Config) (*Gemini, error) {
	if cfg.APIKey == "" {
		return nil, errors.New("GEMINI_API_KEY is required unless SUPPORT_FAKE_LLM=true")
	}
	if cfg.ClassifierModel == "" {
		cfg.ClassifierModel = DefaultModel
	}
	if cfg.WriterModel == "" {
		cfg.WriterModel = cfg.ClassifierModel
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultTimeout
	}
	cc := &genai.ClientConfig{APIKey: cfg.APIKey, Backend: genai.BackendGeminiAPI}
	if cfg.BaseURL != "" {
		cc.HTTPOptions.BaseURL = cfg.BaseURL
	}
	client, err := genai.NewClient(ctx, cc)
	if err != nil {
		return nil, fmt.Errorf("gemini client: %w", err)
	}
	return &Gemini{client: client, cfg: cfg}, nil
}

// Classify asks the classifier model what the message is about.
func (g *Gemini) Classify(ctx context.Context, in domain.ClassifyInput) (domain.Classification, domain.Usage, error) {
	ctx, cancel := context.WithTimeout(ctx, g.cfg.Timeout)
	defer cancel()
	var schema any
	if g.cfg.UseSchema {
		schema = classificationSchema()
	}
	text, usage, err := g.generate(ctx, g.cfg.ClassifierModel, classifierPrompt, classifierInput(in), schema)
	if err != nil {
		return domain.Classification{}, usage, fmt.Errorf("classify: %w", err)
	}
	c, err := parseClassification(text)
	return c, usage, err
}

// Write lets the writer model phrase a reply from the instructions and input render gives it.
func (g *Gemini) Write(ctx context.Context, instructions, input string) (string, domain.Usage, error) {
	ctx, cancel := context.WithTimeout(ctx, g.cfg.Timeout)
	defer cancel()
	text, usage, err := g.generate(ctx, g.cfg.WriterModel, instructions, input, nil)
	if err != nil {
		return "", usage, fmt.Errorf("write: %w", err)
	}
	return strings.TrimSpace(text), usage, nil
}

// generate runs one prompt. With a fallback configured the first model gets
// primaryShare of the time left, so a slow or failing model leaves room for the
// smaller one to answer inside the same deadline.
func (g *Gemini) generate(ctx context.Context, model, system, input string, schema any) (string, domain.Usage, error) {
	first := ctx
	if g.hasFallback(model) {
		if deadline, ok := ctx.Deadline(); ok {
			var cancel context.CancelFunc
			first, cancel = context.WithTimeout(ctx, time.Duration(float64(time.Until(deadline))*primaryShare))
			defer cancel()
		}
	}
	resp, err := g.call(first, model, system, input, schema)
	if err != nil {
		if next := g.retryModel(ctx, model, err); next != "" {
			if next == model {
				pause(ctx, retryPause)
			}
			model = next
			resp, err = g.call(ctx, model, system, input, schema)
		}
	}
	usage := domain.Usage{Model: model}
	if err != nil {
		return "", usage, err
	}
	if m := resp.UsageMetadata; m != nil {
		usage.PromptTokens, usage.CompletionTokens = int(m.PromptTokenCount), int(m.CandidatesTokenCount)
	}
	return resp.Text(), usage, nil
}

func (g *Gemini) call(ctx context.Context, model, system, input string, schema any) (*genai.GenerateContentResponse, error) {
	cfg := &genai.GenerateContentConfig{SystemInstruction: genai.NewContentFromText(system, genai.RoleUser)}
	if schema != nil {
		cfg.ResponseMIMEType, cfg.ResponseJsonSchema = "application/json", schema
	}
	if g.cfg.SendTemperature {
		cfg.Temperature = genai.Ptr[float32](0)
	}
	if g.cfg.ThinkingLevel != "" {
		cfg.ThinkingConfig = &genai.ThinkingConfig{ThinkingLevel: genai.ThinkingLevel(g.cfg.ThinkingLevel)}
	}
	return g.client.Models.GenerateContent(ctx, model, genai.Text(input), cfg)
}

func (g *Gemini) hasFallback(model string) bool {
	return g.cfg.FallbackModel != "" && g.cfg.FallbackModel != model
}

// retryModel picks the model for one more attempt after err, or "" for none.
// A bad request or a cancelled caller is never retried, and neither is a call
// without time left. An overloaded service (429) or a slow first model moves on
// to the fallback model; a server error (5xx) is often gone a moment later, so
// without a fallback the same model is asked once more.
func (g *Gemini) retryModel(ctx context.Context, model string, err error) string {
	if ctx.Err() != nil {
		return ""
	}
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < fallbackBudget {
		return ""
	}
	var apiErr genai.APIError
	switch {
	case errors.As(err, &apiErr):
		switch {
		case g.hasFallback(model) && slices.Contains(retryableStatus, apiErr.Code):
			return g.cfg.FallbackModel
		case apiErr.Code >= http.StatusInternalServerError && slices.Contains(retryableStatus, apiErr.Code):
			return model
		}
	case g.hasFallback(model) && errors.Is(err, context.DeadlineExceeded):
		return g.cfg.FallbackModel
	}
	return ""
}

// retryableStatus are the answers that say "try again" rather than "your request is wrong".
var retryableStatus = []int{http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout}

// retryPause is how long to wait before asking the same model again.
const retryPause = 300 * time.Millisecond

func pause(ctx context.Context, d time.Duration) {
	select {
	case <-time.After(d):
	case <-ctx.Done():
	}
}
