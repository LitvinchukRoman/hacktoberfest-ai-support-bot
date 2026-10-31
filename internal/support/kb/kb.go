// Package kb is the support bot's knowledge base: the site's guides (generated from
// frontend/content by cmd/support-kb) and a FAQ about the platform itself. Search
// is lexical BM25: forty documents need no vectors, and the demo needs no network.
// Below the score threshold nothing is returned, and the bot escalates instead of guessing.
package kb

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/handziurdmytro/skelar/internal/support/domain"
)

//go:embed guides.json
var guidesJSON []byte

//go:embed platform.md
var platformMD string

// HelpPath is where the platform FAQ lives on the site.
const HelpPath = "/help"

const (
	k1 = 1.2
	b  = 0.75
	// MinScore and MinMatched were calibrated on real questions: the S5 question
	// scores about 11 and platform questions 15–24, while one-word or off-topic
	// questions («питання», «Як приготувати борщ?») stay below.
	MinScore   = 5.0
	MinMatched = 2
	stemRunes  = 6
)

type document struct {
	hit    domain.KBHit
	terms  map[string]int
	length int
}

// Index is a searchable set of documents. It is read-only after construction.
type Index struct {
	docs   []document
	df     map[string]int
	avgLen float64
}

var (
	defaultIndex *Index
	defaultErr   error
	defaultOnce  sync.Once
)

// Default is the index over the embedded guides and platform FAQ.
func Default() (*Index, error) {
	defaultOnce.Do(func() {
		var guides []Guide
		if err := json.Unmarshal(guidesJSON, &guides); err != nil {
			defaultErr = fmt.Errorf("kb: guides.json: %w", err)
			return
		}
		defaultIndex = New(guides, ParseFAQ(platformMD))
	})
	return defaultIndex, defaultErr
}

// New indexes guides and the platform FAQ. A guide is one document, and each of
// its FAQ answers is another, so a precise answer can win over a whole article.
func New(guides []Guide, platform []QA) *Index {
	idx := &Index{df: map[string]int{}}
	add := func(hit domain.KBHit, weighted ...string) {
		d := document{hit: hit, terms: map[string]int{}}
		for _, text := range weighted {
			for _, t := range Tokens(text) {
				d.terms[t]++
				d.length++
			}
		}
		idx.docs = append(idx.docs, d)
	}
	for _, g := range guides {
		add(domain.KBHit{Path: g.Path, Title: g.Title, Snippet: clip(g.Description, 300)},
			g.Title, g.Title, g.Title, g.Description, g.Description, g.Text)
		for _, qa := range g.FAQ {
			add(domain.KBHit{Path: g.Path, Title: g.Title, Snippet: clip(qa.A, 300)}, qa.Q, qa.Q, qa.Q, qa.A)
		}
	}
	for _, qa := range platform {
		add(domain.KBHit{Path: HelpPath, Title: qa.Q, Snippet: clip(qa.A, 300)}, qa.Q, qa.Q, qa.Q, qa.A)
	}
	total := 0
	for _, d := range idx.docs {
		total += d.length
		for t := range d.terms {
			idx.df[t]++
		}
	}
	if len(idx.docs) > 0 {
		idx.avgLen = float64(total) / float64(len(idx.docs))
	}
	return idx
}

// Search returns up to limit hits for the question, best first, one per page.
func (idx *Index) Search(query string, limit int) []domain.KBHit {
	q := Tokens(query)
	slices.Sort(q)
	q = slices.Compact(q)
	n := float64(len(idx.docs))
	type scored struct {
		hit     domain.KBHit
		score   float64
		matched int
	}
	var found []scored
	for _, d := range idx.docs {
		s := scored{hit: d.hit}
		for _, t := range q {
			tf := float64(d.terms[t])
			if tf == 0 {
				continue
			}
			df := float64(idx.df[t])
			idf := math.Log(1 + (n-df+0.5)/(df+0.5))
			s.score += idf * tf * (k1 + 1) / (tf + k1*(1-b+b*float64(d.length)/idx.avgLen))
			s.matched++
		}
		if s.score >= MinScore && s.matched >= min(MinMatched, len(q)) {
			found = append(found, s)
		}
	}
	sort.SliceStable(found, func(i, j int) bool { return found[i].score > found[j].score })
	out := []domain.KBHit{}
	seen := map[string]bool{}
	for _, s := range found {
		if seen[s.hit.Path] && s.hit.Path != HelpPath {
			continue
		}
		seen[s.hit.Path] = true
		s.hit.Score = math.Round(s.score*100) / 100
		out = append(out, s.hit)
		if len(out) == limit {
			break
		}
	}
	return out
}

var stopwords = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`і й та а але або в у на з із зі до від по за для про при що це цей ця ці як чи не ні так же ж би б то там тут
		я ти ми ви він вона воно вони мене мені мій моя моє мої твій твоя твоє свій своя своє свою його її їх є був була було буде можна треба
		чому коли де хто який яка яке які якщо щоб уже вже ще дуже також
		the a an to of in on for and or is are how what why do does can i my me you your it`) {
		stopwords[w] = true
	}
}

// Tokens lower-cases text, splits it into words, drops stopwords and keeps the
// first six letters of each word: a crude stemmer that is enough for Ukrainian
// endings (команда, команду, команди → команд).
func Tokens(text string) []string {
	text = strings.NewReplacer("'", "", "’", "", "ʼ", "").Replace(strings.ToLower(text))
	words := strings.FieldsFunc(text, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	out := make([]string, 0, len(words))
	for _, w := range words {
		if stopwords[w] || len([]rune(w)) < 2 {
			continue
		}
		if r := []rune(w); len(r) > stemRunes {
			w = string(r[:stemRunes])
		}
		out = append(out, w)
	}
	return out
}

// ParseFAQ reads Markdown where every "## " heading is a question and the text under it the answer.
func ParseFAQ(md string) []QA {
	var out []QA
	var cur *QA
	var answer []string
	flush := func() {
		if cur != nil {
			if cur.A = plain(strings.Join(answer, " ")); cur.A != "" {
				out = append(out, *cur)
			}
		}
	}
	for _, line := range strings.Split(md, "\n") {
		switch {
		case strings.HasPrefix(line, "## "):
			flush()
			cur, answer = &QA{Q: strings.TrimSpace(line[3:])}, nil
		case cur != nil && !strings.HasPrefix(line, "<!--"):
			answer = append(answer, line)
		}
	}
	flush()
	return out
}
