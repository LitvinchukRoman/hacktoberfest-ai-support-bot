package llm

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"

	"github.com/handziurdmytro/skelar/internal/support/domain"
)

var errorCodeChars = regexp.MustCompile(`^[A-Z0-9_-]{3,40}$`)

// extractJSON returns the object inside a model answer, dropping a ```json fence
// or any words around the outermost braces. A clean answer comes back unchanged.
func extractJSON(s string) string {
	s = strings.TrimSpace(s)
	start, end := strings.Index(s, "{"), strings.LastIndex(s, "}")
	if start < 0 || end < start {
		return s
	}
	return s[start : end+1]
}

func parseClassification(text string) (domain.Classification, error) {
	var c domain.Classification
	if err := json.Unmarshal([]byte(extractJSON(text)), &c); err != nil {
		return domain.Classification{}, fmt.Errorf("%w: %w", ErrBadOutput, err)
	}
	return normalize(c), nil
}

// normalize makes whatever the model returned safe to act on. The schema is a
// safety net, not something the code relies on: an unknown intent or a confidence
// outside [0, 1] becomes "unknown" with confidence 0, which guard escalates.
func normalize(c domain.Classification) domain.Classification {
	if !c.Intent.Valid() || math.IsNaN(c.Confidence) || c.Confidence < 0 || c.Confidence > 1 {
		c.Intent, c.Confidence = domain.IntentUnknown, 0
	}
	if !c.Emotion.Valid() {
		c.Emotion = domain.EmotionNeutral
	}
	c.Language = truncateRunes(strings.ToLower(strings.TrimSpace(c.Language)), 8)
	if c.Language == "" {
		c.Language = "uk"
	}
	c.IdeaHint = truncateRunes(strings.TrimSpace(c.IdeaHint), 120)
	if c.ErrorCode = strings.ToUpper(strings.TrimSpace(c.ErrorCode)); !errorCodeChars.MatchString(c.ErrorCode) {
		c.ErrorCode = ""
	}
	c.UserQuestions = cleanList(c.UserQuestions, 5, 300, false)
	c.Signals = cleanList(c.Signals, 5, 40, true)
	c.Reasoning = truncateRunes(strings.TrimSpace(c.Reasoning), 500)
	return c
}

func cleanList(items []string, maxItems, maxRunes int, lower bool) []string {
	out := []string{}
	for _, s := range items {
		s = strings.TrimSpace(s)
		if lower {
			s = strings.ToLower(s)
		}
		if s == "" || slices.Contains(out, s) {
			continue
		}
		out = append(out, truncateRunes(s, maxRunes))
		if len(out) == maxItems {
			break
		}
	}
	return out
}
