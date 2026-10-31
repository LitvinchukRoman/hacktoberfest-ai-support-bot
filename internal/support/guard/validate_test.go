package guard

import (
	"strings"
	"testing"

	"github.com/handziurdmytro/skelar/internal/support/domain"
)

func TestValidate(t *testing.T) {
	autoDecision := domain.Decision{Autonomy: domain.AutonomyAuto}
	approveDecision := domain.Decision{Autonomy: domain.AutonomyApprove}

	tests := []struct {
		name       string
		reply      string
		decision   domain.Decision
		wantOK     bool
		wantFailed string
	}{
		{"clean reply passes", "Твоя ідея на премодерації ще 3 години.", autoDecision, true, ""},
		{"empty reply fails", "", autoDecision, false, "non_empty"},
		{"too long reply fails", strings.Repeat("а", 1501), autoDecision, false, "non_empty"},
		{"exactly 1500 is fine", strings.Repeat("а", 1500), autoDecision, true, ""},
		{"leaked email fails", "Напишіть мені на olena@example.com", autoDecision, false, "no_personal_data"},
		{"internal rule name leaks", "За правилом G_self_harm ми тебе ескалювали.", autoDecision, false, "no_internal_terms"},
		{"classifier word leaks", "Наш classifier вирішив інакше.", autoDecision, false, "no_internal_terms"},
		{"unfilled template placeholder", "Привіт {{name}}, твоя ідея на премодерації.", autoDecision, false, "no_template_leftovers"},
		{"external link", "Детальніше на https://example.com/help", autoDecision, false, "no_external_links"},
		{"www link without scheme", "Детальніше на www.example.com", autoDecision, false, "no_external_links"},
		{"overpromise on auto is fine - action really ran", "Я відновив твою ідею.", autoDecision, true, ""},
		{"overpromise on approve is a lie - nothing executed yet", "Я відновив твою ідею.", approveDecision, false, "no_overpromise"},
		{"free days said before a colleague approved", "Я додав тобі 7 днів «Про».", approveDecision, false, "no_overpromise"},
		{"a cancellation is never the bot's to report", "Готово, підписку скасовано.", autoDecision, false, "no_billing_promise"},
		{"nor is a refund", "Я повернув кошти на картку.", autoDecision, false, "no_billing_promise"},
		{"talking about cancelling is fine", "Рішення про скасування підписки й повернення коштів ухвалює менеджер.", approveDecision, true, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, checks := Validate(tt.reply, tt.decision)
			if ok != tt.wantOK {
				t.Fatalf("Validate() ok = %v, want %v (checks=%+v)", ok, tt.wantOK, checks)
			}
			if got := FailedCheck(checks); got != tt.wantFailed {
				t.Fatalf("FailedCheck() = %q, want %q", got, tt.wantFailed)
			}
		})
	}
}
