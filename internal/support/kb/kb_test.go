package kb

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func index(t *testing.T) *Index {
	t.Helper()
	idx, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	return idx
}

func TestSearchFindsTheRightPage(t *testing.T) {
	cases := []struct{ query, path, title string }{
		{"Як знайти команду для проєкту?", "/campus/yak-znaity-komandu-dlia-proiektu", ""}, // S5
		{"де шукати тіммейтів на хакатон", "/campus/yak-znaity-komandu-dlia-proiektu", ""},
		{"які є стипендії", "/campus/stypendii-ta-hranty-naukma", ""},
		{"скільки ідей можна опублікувати за день", HelpPath, "Скільки ідей можна опублікувати за день?"},
		{"Як створити ідею?", HelpPath, "Як створити й опублікувати ідею?"},
		{"як увімкнути сповіщення на айфоні", HelpPath, "Як увімкнути сповіщення на телефоні?"},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			hits := index(t).Search(tc.query, 3)
			if len(hits) == 0 {
				t.Fatal("no hits")
			}
			if hits[0].Path != tc.path || (tc.title != "" && hits[0].Title != tc.title) {
				t.Fatalf("first hit = %s %q (score %.2f)", hits[0].Path, hits[0].Title, hits[0].Score)
			}
		})
	}
}

func TestSearchSaysNothingRatherThanGuess(t *testing.T) {
	for _, q := range []string{"Котра година?", "Як приготувати борщ?", "питання", "", "What is quantum physics?"} {
		if hits := index(t).Search(q, 3); len(hits) > 0 {
			t.Errorf("%q matched %s %q (score %.2f)", q, hits[0].Path, hits[0].Title, hits[0].Score)
		}
	}
}

func TestHitsAreCleanAndOnePerPage(t *testing.T) {
	hits := index(t).Search("Як знайти команду для хакатону чи проєкту?", 3)
	var paths []string
	for _, h := range hits {
		if strings.Contains(h.Snippet, "**") || strings.Contains(h.Snippet, "](") || h.Snippet == "" || h.Title == "" {
			t.Errorf("hit is not clean text: %+v", h)
		}
		if slices.Contains(paths, h.Path) {
			t.Errorf("page %s returned twice", h.Path)
		}
		paths = append(paths, h.Path)
	}
}

func TestTokensStemUkrainianEndings(t *testing.T) {
	if a, b, c := Tokens("команда"), Tokens("команду"), Tokens("команди"); !slices.Equal(a, b) || !slices.Equal(b, c) {
		t.Fatalf("%v %v %v", a, b, c)
	}
	if got := Tokens("Як об'єднати профілі, і що це?"); !slices.Equal(got, []string{"обєдна", "профіл"}) {
		t.Fatalf("tokens = %v", got)
	}
}

func TestParseGuide(t *testing.T) {
	src := []byte("---\ntitle: \"Як знайти **команду**\"\ndescription: \"Опис\"\nfaq:\n  - q: \"Скільки людей?\"\n    a: \"Від [2](/x) до 6.\"\n---\n\n## Розділ\n\n- **Курс.** Дивіться [огляд](/campus/orgs).\n\n| Роль | Що |\n|---|---|\n| Лід | Дедлайни |\n")
	g, err := ParseGuide("campus", "team", src)
	if err != nil {
		t.Fatal(err)
	}
	want := Guide{Path: "/campus/team", Title: "Як знайти команду", Description: "Опис",
		Text: "Розділ Курс. Дивіться огляд. Роль Що Лід Дедлайни", FAQ: []QA{{Q: "Скільки людей?", A: "Від 2 до 6."}}}
	if !reflectEqual(g, want) {
		t.Fatalf("got  %+v\nwant %+v", g, want)
	}
	if _, err := ParseGuide("campus", "x", []byte("no frontmatter")); err == nil {
		t.Fatal("a file without frontmatter must be rejected")
	}
}

func reflectEqual(a, b Guide) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}

func TestPlatformFAQParses(t *testing.T) {
	faq := ParseFAQ(platformMD)
	if len(faq) < 12 {
		t.Fatalf("platform FAQ has %d entries, want at least 12", len(faq))
	}
	for _, qa := range faq {
		if !strings.HasSuffix(qa.Q, "?") || qa.A == "" || strings.Contains(qa.A, "<!--") {
			t.Errorf("bad entry %+v", qa)
		}
	}
}

// The embedded index must match the articles; regenerate with make support-kb FRONTEND=<fantasm frontend>.
func TestGuidesJSONIsUpToDate(t *testing.T) {
	frontend := os.Getenv("FANTASM_FRONTEND")
	if frontend == "" {
		t.Skip("set FANTASM_FRONTEND to a fantasm frontend checkout to compare guides.json with its articles")
	}
	root := filepath.Join(frontend, "content")
	var embedded []Guide
	if err := json.Unmarshal(guidesJSON, &embedded); err != nil {
		t.Fatal(err)
	}
	for _, g := range embedded {
		parts := strings.Split(strings.TrimPrefix(g.Path, "/"), "/")
		src, err := os.ReadFile(filepath.Join(root, parts[0], parts[1]+".mdx"))
		if err != nil {
			t.Fatalf("%s: %v (regenerate guides.json)", g.Path, err)
		}
		fresh, err := ParseGuide(parts[0], parts[1], src)
		if err != nil || !reflectEqual(fresh, g) {
			t.Fatalf("%s is stale: regenerate guides.json with make support-kb FRONTEND=<fantasm frontend>", g.Path)
		}
	}
	mdx, _ := filepath.Glob(filepath.Join(root, "*", "*.mdx"))
	articles := 0
	for _, m := range mdx {
		if filepath.Base(m) != "index.mdx" {
			articles++
		}
	}
	if articles != len(embedded) {
		t.Fatalf("%d articles but %d in guides.json: regenerate it", articles, len(embedded))
	}
}
