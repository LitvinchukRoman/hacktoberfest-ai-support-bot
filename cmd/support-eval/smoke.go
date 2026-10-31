package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/handziurdmytro/skelar/internal/support/domain"
	"github.com/handziurdmytro/skelar/internal/support/llm"
)

// runSmoke sends the S1 message once per request setting and shows what the model
// accepted. Its result sets SUPPORT_LLM_SCHEMA, SUPPORT_LLM_TEMPERATURE and SUPPORT_LLM_THINKING.
func runSmoke(ctx context.Context, base llm.Config, w io.Writer) error {
	if base.APIKey == "" {
		return fmt.Errorf("GEMINI_API_KEY is not set")
	}
	primary, fallback := base.ClassifierModel, base.FallbackModel
	variants := []struct {
		name   string
		mutate func(*llm.Config)
	}{
		{"schema + temperature 0", func(c *llm.Config) { c.UseSchema, c.SendTemperature = true, true }},
		{"plain JSON prompt", func(c *llm.Config) { c.UseSchema, c.SendTemperature = false, false }},
		{"schema + thinking MINIMAL", func(c *llm.Config) { c.UseSchema, c.SendTemperature, c.ThinkingLevel = true, true, "MINIMAL" }},
		{"fallback model, schema", func(c *llm.Config) { c.ClassifierModel, c.UseSchema, c.SendTemperature = fallback, true, true }},
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SETTING\tMODEL\tRESULT\tLATENCY\tTOKENS IN/OUT")
	in := domain.ClassifyInput{Text: "Чому моєї ідеї немає в стрічці?", Page: "/ideas"}
	for i, v := range variants {
		if i > 0 {
			time.Sleep(2 * time.Second)
		}
		cfg := base
		cfg.ClassifierModel, cfg.FallbackModel, cfg.ThinkingLevel = primary, "", ""
		v.mutate(&cfg)
		g, err := llm.NewGemini(ctx, cfg)
		if err != nil {
			return err
		}
		start := time.Now()
		c, usage, err := g.Classify(ctx, in)
		outcome := fmt.Sprintf("ok: %s %.2f", c.Intent, c.Confidence)
		if err != nil {
			outcome = "FAILED: " + oneLine(err.Error(), 90)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d/%d\n", v.name, cfg.ClassifierModel, outcome, time.Since(start).Round(time.Millisecond), usage.PromptTokens, usage.CompletionTokens)
	}
	time.Sleep(2 * time.Second)
	g, err := llm.NewGemini(ctx, base)
	if err != nil {
		return err
	}
	start := time.Now()
	text, usage, err := g.Write(ctx, "Answer in one short Ukrainian sentence.", "Скажи, що підтримка працює.")
	outcome := "ok: " + oneLine(text, 60)
	if err != nil {
		outcome = "FAILED: " + oneLine(err.Error(), 90)
	}
	fmt.Fprintf(tw, "writer\t%s\t%s\t%s\t%d/%d\n", usage.Model, outcome, time.Since(start).Round(time.Millisecond), usage.PromptTokens, usage.CompletionTokens)
	return tw.Flush()
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
