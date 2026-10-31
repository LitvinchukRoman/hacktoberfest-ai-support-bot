package guard

import (
	"slices"
	"testing"
)

func TestRedact(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		wantText string
		wantKind []string
	}{
		{
			name:     "valid card with spaces",
			text:     "pay with 4111 1111 1111 1111 please",
			wantText: "pay with [CARD] please",
			wantKind: []string{"card"},
		},
		{
			name:     "non-luhn 16 digits is not a card, left alone",
			text:     "ref number 1111 1111 1111 1112",
			wantText: "ref number 1111 1111 1111 1112",
			wantKind: nil,
		},
		{
			name:     "phone with international prefix",
			text:     "call me +380 67 123 45 67",
			wantText: "call me [PHONE]",
			wantKind: []string{"phone"},
		},
		{
			name:     "phone with dashes",
			text:     "067-123-45-67 дзвони",
			wantText: "[PHONE] дзвони",
			wantKind: []string{"phone"},
		},
		{
			name:     "iban",
			text:     "переказ на UA213223130000026007233566001 будь ласка",
			wantText: "переказ на [IBAN] будь ласка",
			wantKind: []string{"iban"},
		},
		{
			name:     "email",
			text:     "пишіть olena.doe@example.com",
			wantText: "пишіть [EMAIL]",
			wantKind: []string{"email"},
		},
		{
			name:     "nothing to redact",
			text:     "просто текст без персональних даних",
			wantText: "просто текст без персональних даних",
			wantKind: nil,
		},
		{
			name:     "multiple kinds in one message",
			text:     "картка 4111 1111 1111 1111, email olena@example.com",
			wantText: "картка [CARD], email [EMAIL]",
			wantKind: []string{"card", "email"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Redact(tt.text)
			if got.Text != tt.wantText {
				t.Errorf("Text = %q, want %q", got.Text, tt.wantText)
			}
			if !slices.Equal(got.Found, tt.wantKind) {
				t.Errorf("Found = %v, want %v", got.Found, tt.wantKind)
			}
		})
	}
}

func TestLuhnValid(t *testing.T) {
	tests := []struct {
		digits string
		want   bool
	}{
		{"4111111111111111", true},
		{"1111111111111112", false},
		{"4111111111111112", false},
		{"", false},
		{"411111111111111a", false},
	}
	for _, tt := range tests {
		if got := luhnValid(tt.digits); got != tt.want {
			t.Errorf("luhnValid(%q) = %v, want %v", tt.digits, got, tt.want)
		}
	}
}
