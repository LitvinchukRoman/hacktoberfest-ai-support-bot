package kb

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// Guide is one article from frontend/content, reduced to what search needs.
type Guide struct {
	Path        string `json:"path"` // /<hub>/<slug>, the page on the site
	Title       string `json:"title"`
	Description string `json:"description"`
	Text        string `json:"text"` // the start of the article as plain text
	FAQ         []QA   `json:"faq,omitempty"`
}

type QA struct {
	Q string `json:"q"`
	A string `json:"a"`
}

// TextRunes is how much of an article's body is kept.
const TextRunes = 1500

var (
	mdLink      = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)`)
	mdMarks     = regexp.MustCompile("[*_`#>|]+")
	mdListStart = regexp.MustCompile(`(?m)^\s*(?:[-+]|\d+\.)\s+`)
	mdRule      = regexp.MustCompile(`(?m)^\s*-{3,}\s*$|^\|?\s*:?-{3,}.*$`)
	spaces      = regexp.MustCompile(`\s+`)
)

// ParseGuide reads an MDX article: YAML frontmatter between "---" lines, then Markdown.
func ParseGuide(hub, slug string, src []byte) (Guide, error) {
	src = bytes.ReplaceAll(src, []byte("\r\n"), []byte("\n"))
	if !bytes.HasPrefix(src, []byte("---\n")) {
		return Guide{}, errors.New("no frontmatter")
	}
	end := bytes.Index(src[4:], []byte("\n---\n"))
	if end < 0 {
		return Guide{}, errors.New("unterminated frontmatter")
	}
	var front struct {
		Title       string `yaml:"title"`
		Description string `yaml:"description"`
		FAQ         []QA   `yaml:"faq"`
	}
	if err := yaml.Unmarshal(src[4:4+end], &front); err != nil {
		return Guide{}, fmt.Errorf("frontmatter: %w", err)
	}
	if strings.TrimSpace(front.Title) == "" {
		return Guide{}, errors.New("guide has no title")
	}
	faq := make([]QA, 0, len(front.FAQ))
	for _, qa := range front.FAQ {
		if q, a := plain(qa.Q), plain(qa.A); q != "" && a != "" {
			faq = append(faq, QA{Q: q, A: a})
		}
	}
	return Guide{
		Path:        "/" + hub + "/" + slug,
		Title:       plain(front.Title),
		Description: plain(front.Description),
		Text:        clip(plain(string(src[4+end+5:])), TextRunes),
		FAQ:         faq,
	}, nil
}

// plain turns Markdown into one line of text: links keep their words, marks and tables go.
func plain(md string) string {
	s := mdLink.ReplaceAllString(md, "$1")
	s = mdRule.ReplaceAllString(s, " ")
	s = mdListStart.ReplaceAllString(s, "")
	s = mdMarks.ReplaceAllString(s, " ")
	return strings.TrimSpace(spaces.ReplaceAllString(s, " "))
}

// clip cuts text to at most n runes, at the end of a sentence when there is one in the last third.
func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)[:n]
	cut := string(r)
	if i := strings.LastIndexAny(cut, ".!?"); i > len(cut)*2/3 {
		return cut[:i+1]
	}
	return strings.TrimSpace(cut) + "…"
}
