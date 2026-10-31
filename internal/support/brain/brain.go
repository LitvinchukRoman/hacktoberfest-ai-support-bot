// Package brain assembles the support bot's AI core into the domain.Brain the
// orchestrator runs: Gemma 4 when a key is configured, otherwise the keyword
// classifier and templates, which work offline.
package brain

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/handziurdmytro/skelar/internal/support/domain"
	"github.com/handziurdmytro/skelar/internal/support/llm"
	"github.com/handziurdmytro/skelar/internal/support/policy"
	"github.com/handziurdmytro/skelar/internal/support/render"
)

type Config struct {
	Fake   bool // no model: llm.Fake and render.Templater
	LLM    llm.Config
	Policy policy.Config // zero means policy.DefaultConfig
}

// FromEnv reads the AI core's settings. Without GEMINI_API_KEY, or with
// SUPPORT_FAKE_LLM=true, the bot runs offline on the keyword classifier.
//
//	GEMINI_API_KEY            key from Google AI Studio; environment only, never logged
//	SUPPORT_CLASSIFIER_MODEL  default gemma-4-26b-a4b-it
//	SUPPORT_WRITER_MODEL      default gemma-4-26b-a4b-it
//	SUPPORT_FALLBACK_MODEL    optional second model; empty = fall back to the keyword classifier
//	SUPPORT_LLM_TEMPERATURE   default true (sends temperature 0)
//	SUPPORT_LLM_SCHEMA        default true (JSON by MIME type and schema)
//	SUPPORT_LLM_THINKING      default MINIMAL
//	SUPPORT_LLM_TIMEOUT       default 8s, one whole call
func FromEnv() Config {
	str := func(key, fallback string) string {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
		return fallback
	}
	timeout := 8 * time.Second
	if d, err := time.ParseDuration(os.Getenv("SUPPORT_LLM_TIMEOUT")); err == nil && d >= time.Second && d <= time.Minute {
		timeout = d
	}
	key := strings.TrimSpace(os.Getenv("GEMINI_API_KEY"))
	return Config{
		Fake: str("SUPPORT_FAKE_LLM", "false") == "true" || key == "",
		LLM: llm.Config{
			APIKey:          key,
			ClassifierModel: str("SUPPORT_CLASSIFIER_MODEL", llm.DefaultModel),
			WriterModel:     str("SUPPORT_WRITER_MODEL", llm.DefaultModel),
			FallbackModel:   strings.TrimSpace(os.Getenv("SUPPORT_FALLBACK_MODEL")),
			SendTemperature: str("SUPPORT_LLM_TEMPERATURE", "true") == "true",
			UseSchema:       str("SUPPORT_LLM_SCHEMA", "true") == "true",
			ThinkingLevel:   strings.ToUpper(str("SUPPORT_LLM_THINKING", "MINIMAL")),
			Timeout:         timeout,
		},
	}
}

// New builds the brain around a facts source. Decide is always the pure policy.
func New(ctx context.Context, cfg Config, facts domain.FactsSource) (domain.Brain, error) {
	rules := cfg.Policy
	if rules == (policy.Config{}) {
		rules = policy.DefaultConfig
	}
	b := domain.Brain{
		Classifier: llm.Fake{},
		Facts:      facts,
		Decide:     func(c domain.Classification, f domain.Facts) domain.Decision { return policy.Decide(c, f, rules) },
		Renderer:   render.Templater{},
	}
	if cfg.Fake {
		return b, nil
	}
	gemini, err := llm.NewGemini(ctx, cfg.LLM)
	if err != nil {
		return domain.Brain{}, err
	}
	b.Classifier, b.Renderer = gemini, render.NewLLMRenderer(gemini)
	return b, nil
}
