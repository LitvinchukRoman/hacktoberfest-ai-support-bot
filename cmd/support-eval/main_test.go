package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/handziurdmytro/skelar/internal/support/domain"
	"github.com/handziurdmytro/skelar/internal/support/llm"
)

func TestGoldsetCoversEveryIntent(t *testing.T) {
	set, err := loadSet("testdata/goldset.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if len(set) < 40 {
		t.Fatalf("goldset has %d lines, want at least 40", len(set))
	}
	seen := map[domain.Intent]int{}
	for _, ex := range set {
		seen[ex.Intent]++
	}
	for _, i := range domain.Intents {
		if seen[i] < 2 {
			t.Errorf("intent %s has %d examples, want at least 2", i, seen[i])
		}
	}
}

// The offline demo runs on the keyword classifier, so it must understand every demo scenario.
func TestKeywordFallbackGetsEveryDemoScenarioRight(t *testing.T) {
	set, err := loadSet("testdata/goldset.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range evaluate(context.Background(), llm.Fake{}, set, 0) {
		if strings.HasPrefix(r.Tag, "S") && r.served.Intent != r.Intent {
			t.Errorf("%s: want %s, got %s", r.Tag, r.Intent, r.served.Intent)
		}
	}
}

func TestReportPrintsTheNumbers(t *testing.T) {
	set, _ := loadSet("testdata/goldset.jsonl")
	var out bytes.Buffer
	report(&out, evaluate(context.Background(), llm.Fake{}, set, 0))
	for _, want := range []string{"Intent accuracy:", "Goes to a person:", "Latency p50 / p95:", "Confusion", "fake ×"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report lacks %q:\n%s", want, out.String())
		}
	}
}
