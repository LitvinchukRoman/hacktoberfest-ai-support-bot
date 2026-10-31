package brain

import (
	"context"
	"testing"
	"time"

	"github.com/handziurdmytro/skelar/internal/support/domain"
	"github.com/handziurdmytro/skelar/internal/support/llm"
	"github.com/handziurdmytro/skelar/internal/support/render"
	"github.com/handziurdmytro/skelar/internal/support/supporttest"
)

func TestFakeBrainWorksOffline(t *testing.T) {
	b, err := New(context.Background(), Config{Fake: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := b.Classifier.(llm.Fake); !ok {
		t.Fatalf("classifier = %T", b.Classifier)
	}
	if _, ok := b.Renderer.(render.Templater); !ok {
		t.Fatalf("renderer = %T", b.Renderer)
	}
	// S1 end to end without a model or a database.
	c, _, _ := b.Classifier.Classify(context.Background(), domain.ClassifyInput{Text: "Чому моєї ідеї немає в стрічці?"})
	d := b.Decide(c, domain.Facts{Now: supporttest.Now, User: supporttest.GoogleNewcomer(), Idea: supporttest.PendingIdea(3 * time.Hour)})
	reply, _, err := b.Renderer.Render(context.Background(), domain.RenderInput{TemplateID: d.TemplateID, Slots: d.Slots, Emotion: c.Emotion})
	if err != nil || d.Rule != "V2_premoderation" || reply == "" {
		t.Fatalf("rule %s, reply %q, err %v", d.Rule, reply, err)
	}
}

func TestGemmaBrainNeedsAKey(t *testing.T) {
	if _, err := New(context.Background(), Config{}, nil); err == nil {
		t.Fatal("a brain without a key and without Fake must not start")
	}
	b, err := New(context.Background(), Config{LLM: llm.Config{APIKey: "k"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := b.Classifier.(*llm.Gemini); !ok {
		t.Fatalf("classifier = %T", b.Classifier)
	}
}

func TestFromEnvDefaultsToTheOfflineBotWithoutAKey(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	if cfg := FromEnv(); !cfg.Fake {
		t.Fatalf("no key must mean the offline bot: %+v", cfg)
	}
}

func TestFromEnvReadsTheModelSettings(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "k")
	t.Setenv("SUPPORT_FAKE_LLM", "")
	t.Setenv("SUPPORT_LLM_THINKING", "low")
	t.Setenv("SUPPORT_LLM_TIMEOUT", "12s")
	t.Setenv("SUPPORT_FALLBACK_MODEL", "gemma-4-31b-it")
	cfg := FromEnv()
	if cfg.Fake || cfg.LLM.ClassifierModel != llm.DefaultModel || cfg.LLM.WriterModel != llm.DefaultModel ||
		cfg.LLM.FallbackModel != "gemma-4-31b-it" || cfg.LLM.ThinkingLevel != "LOW" || cfg.LLM.Timeout != 12*time.Second ||
		!cfg.LLM.UseSchema || !cfg.LLM.SendTemperature {
		t.Fatalf("cfg = %+v", cfg)
	}
	t.Setenv("SUPPORT_FAKE_LLM", "true")
	if !FromEnv().Fake {
		t.Fatal("SUPPORT_FAKE_LLM=true forces the offline bot even with a key")
	}
}
