// Command support-eval measures the support classifier on a hand-written goldset:
//
//	go run ./cmd/support-eval           # Gemma 4; GEMINI_API_KEY and SUPPORT_* from the environment
//	go run ./cmd/support-eval -fake     # the keyword classifier: no network, runs in CI
//	go run ./cmd/support-eval -smoke    # one request per model setting: what does Gemma accept?
//
// It prints intent accuracy, a confusion matrix, how often a message would go to a
// person, latency and tokens. Message texts are printed only for wrong answers.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/handziurdmytro/skelar/internal/support/brain"
	"github.com/handziurdmytro/skelar/internal/support/domain"
	"github.com/handziurdmytro/skelar/internal/support/llm"
)

// minConfidence mirrors guard's threshold: below it the message goes to a person.
const minConfidence = 0.6

type example struct {
	Tag    string        `json:"tag"`
	Intent domain.Intent `json:"intent"`
	Text   string        `json:"text"`
}

type result struct {
	example
	got     domain.Classification
	served  domain.Classification // what the bot acts on: the fake classification when the model failed
	usage   domain.Usage
	latency time.Duration
	err     error
}

func main() {
	set := flag.String("set", "cmd/support-eval/testdata/goldset.jsonl", "goldset, one JSON object per line")
	fake := flag.Bool("fake", false, "use the keyword classifier instead of the model")
	smoke := flag.Bool("smoke", false, "check which request settings the model accepts, then exit")
	rps := flag.Float64("rps", 0.5, "requests per second to the model (free tiers are slow)")
	flag.Parse()

	if err := run(*set, *fake, *smoke, *rps, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "support-eval:", err)
		os.Exit(1)
	}
}

func run(setPath string, fake, smoke bool, rps float64, out io.Writer) error {
	ctx := context.Background()
	cfg := brain.FromEnv()
	llmCfg := cfg.LLM
	if smoke {
		return runSmoke(ctx, llmCfg, out)
	}
	set, err := loadSet(setPath)
	if err != nil {
		return err
	}
	var classifier domain.Classifier = llm.Fake{}
	if !fake {
		if llmCfg.APIKey == "" {
			return errors.New("GEMINI_API_KEY is not set (use -fake for the keyword classifier)")
		}
		if classifier, err = llm.NewGemini(ctx, llmCfg); err != nil {
			return err
		}
	} else {
		rps = 0
	}
	report(out, evaluate(ctx, classifier, set, rps))
	return nil
}

func loadSet(path string) ([]example, error) {
	f, err := os.Open(path) // #nosec G304 -- the operator names the goldset
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var set []example
	sc := bufio.NewScanner(f)
	for line := 1; sc.Scan(); line++ {
		if strings.TrimSpace(sc.Text()) == "" {
			continue
		}
		var ex example
		if err := json.Unmarshal(sc.Bytes(), &ex); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, line, err)
		}
		if !ex.Intent.Valid() || strings.TrimSpace(ex.Text) == "" {
			return nil, fmt.Errorf("%s:%d: bad intent %q or empty text", path, line, ex.Intent)
		}
		set = append(set, ex)
	}
	return set, sc.Err()
}

func evaluate(ctx context.Context, c domain.Classifier, set []example, rps float64) []result {
	var gap time.Duration
	if rps > 0 {
		gap = time.Duration(float64(time.Second) / rps)
	}
	out := make([]result, 0, len(set))
	for i, ex := range set {
		if i > 0 && gap > 0 {
			time.Sleep(gap)
		}
		start := time.Now()
		got, usage, err := c.Classify(ctx, domain.ClassifyInput{Text: ex.Text})
		r := result{example: ex, got: got, served: got, usage: usage, latency: time.Since(start), err: err}
		if err != nil {
			r.served, _, _ = llm.Fake{}.Classify(ctx, domain.ClassifyInput{Text: ex.Text})
		}
		out = append(out, r)
	}
	return out
}

type summary struct {
	n, correct, servedCorrect, toPerson, errors int
	latencies                                   []time.Duration
	promptTokens, completionTokens              int
	models                                      map[string]int
}

func summarize(results []result) summary {
	s := summary{n: len(results), models: map[string]int{}}
	for _, r := range results {
		if r.err != nil {
			s.errors++
		} else if r.got.Intent == r.Intent {
			s.correct++
		}
		if r.served.Intent == r.Intent {
			s.servedCorrect++
		}
		if goesToPerson(r.served) {
			s.toPerson++
		}
		s.latencies = append(s.latencies, r.latency)
		s.promptTokens += r.usage.PromptTokens
		s.completionTokens += r.usage.CompletionTokens
		s.models[r.usage.Model]++
	}
	slices.Sort(s.latencies)
	return s
}

// goesToPerson approximates guard: operations intents, low confidence and injection attempts go to people.
func goesToPerson(c domain.Classification) bool {
	switch c.Intent {
	case domain.IntentAccountData, domain.IntentHumanRequest, domain.IntentLegal, domain.IntentOffensive, domain.IntentUnknown:
		return true
	}
	return c.Confidence < minConfidence || slices.Contains(c.Signals, domain.SignalPromptInjection)
}

func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	return sorted[min(len(sorted)-1, int(p*float64(len(sorted))))]
}

func pct(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return 100 * float64(a) / float64(b)
}

func report(w io.Writer, results []result) {
	s := summarize(results)
	fmt.Fprintf(w, "Intent accuracy:      %.1f%% (%d/%d)\n", pct(s.correct, s.n), s.correct, s.n)
	fmt.Fprintf(w, "As served:            %.1f%% (model errors answered by the keyword fallback)\n", pct(s.servedCorrect, s.n))
	fmt.Fprintf(w, "Goes to a person:     %.1f%% (%d/%d)\n", pct(s.toPerson, s.n), s.toPerson, s.n)
	fmt.Fprintf(w, "Model errors:         %d\n", s.errors)
	fmt.Fprintf(w, "Latency p50 / p95:    %s / %s\n", percentile(s.latencies, 0.5).Round(time.Millisecond), percentile(s.latencies, 0.95).Round(time.Millisecond))
	fmt.Fprintf(w, "Tokens in / out:      %d / %d\n", s.promptTokens, s.completionTokens)
	models := make([]string, 0, len(s.models))
	for m, n := range s.models {
		models = append(models, fmt.Sprintf("%s ×%d", m, n))
	}
	sort.Strings(models)
	fmt.Fprintf(w, "Answered by:          %s\n\n", strings.Join(models, ", "))

	fmt.Fprintln(w, "Confusion (rows: expected, columns: got; only intents that occur):")
	var intents []domain.Intent
	for _, i := range domain.Intents {
		for _, r := range results {
			if r.Intent == i || r.served.Intent == i {
				intents = append(intents, i)
				break
			}
		}
	}
	tw := tabwriter.NewWriter(w, 0, 0, 1, ' ', tabwriter.AlignRight)
	fmt.Fprint(tw, "\t")
	for i := range intents {
		fmt.Fprintf(tw, "%d\t", i+1)
	}
	fmt.Fprintln(tw)
	for i, want := range intents {
		fmt.Fprintf(tw, "%d %s\t", i+1, want)
		for _, got := range intents {
			n := 0
			for _, r := range results {
				if r.Intent == want && r.served.Intent == got {
					n++
				}
			}
			cell := "."
			if n > 0 {
				cell = fmt.Sprint(n)
			}
			fmt.Fprintf(tw, "%s\t", cell)
		}
		fmt.Fprintln(tw)
	}
	_ = tw.Flush()

	fmt.Fprintln(w, "\nWrong answers:")
	for _, r := range results {
		if r.served.Intent != r.Intent {
			note := ""
			if r.err != nil {
				note = " (model error, keyword fallback)"
			}
			fmt.Fprintf(w, "  [%s] want %s, got %s %.2f%s: %s\n", r.Tag, r.Intent, r.served.Intent, r.served.Confidence, note, r.Text)
		}
	}
}
