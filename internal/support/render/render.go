// Package render holds every word the bot says. Policy and guard only name a
// template; Templater fills it deterministically, and LLMRenderer may let the
// model phrase it, falling back to Templater on any error.
package render

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/handziurdmytro/skelar/internal/support/domain"
)

// TemplateModel is the Usage.Model of a reply filled from the catalog without a model.
const TemplateModel = "template"

var (
	ErrUnknownTemplate = errors.New("unknown template")
	ErrMissingSlot     = errors.New("missing template slot")

	slotPattern = regexp.MustCompile(`\{\{(\w+)\}\}`)
)

// Templater fills catalog templates. It is the offline renderer and the fallback for LLMRenderer.
type Templater struct{}

func (Templater) Render(_ context.Context, in domain.RenderInput) (string, domain.Usage, error) {
	text, err := Fill(in.TemplateID, in.Slots)
	if err != nil {
		return "", domain.Usage{}, err
	}
	return withEmpathy(text, in), domain.Usage{Model: TemplateModel}, nil
}

// Fill puts slot values into the template. A missing slot is an error rather than
// a "{{title}}" left in front of a person.
func Fill(templateID string, slots map[string]string) (string, error) {
	tpl, ok := Templates[templateID]
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrUnknownTemplate, templateID)
	}
	var missing []string
	out := slotPattern.ReplaceAllStringFunc(tpl, func(m string) string {
		name := m[2 : len(m)-2]
		v, ok := slots[name]
		if !ok || strings.TrimSpace(v) == "" {
			missing = append(missing, name)
			return m
		}
		if name == "visibility" {
			if words, ok := visibilityWords[v]; ok {
				return words
			}
		}
		return strings.TrimSpace(v)
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("%w: %s needs %s", ErrMissingSlot, templateID, strings.Join(missing, ", "))
	}
	return out, nil
}

// Slots lists the slot names a template needs.
func Slots(templateID string) []string {
	var names []string
	for _, m := range slotPattern.FindAllStringSubmatch(Templates[templateID], -1) {
		names = append(names, m[1])
	}
	return names
}

func withEmpathy(text string, in domain.RenderInput) string {
	if strings.HasPrefix(in.TemplateID, "safety.") {
		return text
	}
	if in.Emotion == domain.EmotionAngry || in.Emotion == domain.EmotionFrustrated {
		return empathyOpener + " " + text
	}
	return text
}
