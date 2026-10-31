package investigate

import (
	"slices"
	"testing"
	"time"

	"github.com/handziurdmytro/skelar/internal/support/domain"
)

var t0 = time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)

func report(age time.Duration, approved int, minute int) domain.ReportFacts {
	return domain.ReportFacts{ReporterAge: age, ReporterApproved: approved, CreatedAt: t0.Add(time.Duration(minute) * time.Minute)}
}

func TestReports(t *testing.T) {
	day, year := 24*time.Hour, 365*24*time.Hour
	cases := []struct {
		name     string
		reports  []domain.ReportFacts
		want     domain.VerdictKind
		evidence []string
	}{
		{
			name: "a brigade of new accounts in 12 minutes is suspected abuse (S2)",
			reports: []domain.ReportFacts{
				report(2*time.Hour, 0, 0), report(3*time.Hour, 0, 3), report(5*time.Hour, 0, 7),
				report(time.Hour, 0, 10), report(year, 0, 12),
			},
			want:     domain.VerdictSuspectedAbuse,
			evidence: []string{"4 з 5 скарг — акаунти, молодші за добу", "усі 5 скарг подано за 12 хв", "у скаржників 0 схвалених ідей"},
		},
		{
			name: "old accounts in a burst without approved ideas are still suspicious",
			reports: []domain.ReportFacts{
				report(year, 0, 0), report(year, 0, 5), report(year, 0, 9),
			},
			want: domain.VerdictSuspectedAbuse,
		},
		{
			name: "established reporters spread over days need a person",
			reports: []domain.ReportFacts{
				report(year, 2, 0), report(year, 1, 600), report(2*year, 5, 3000),
				report(year, 0, 4000), report(year, 3, 5000),
			},
			want: domain.VerdictUnclear,
		},
		{
			name:    "two new accounts are not enough on their own",
			reports: []domain.ReportFacts{report(time.Hour, 0, 0), report(time.Hour, 0, 400), report(year, 4, 900)},
			want:    domain.VerdictUnclear,
		},
		{
			name:    "a burst where a reporter has approved ideas needs a person",
			reports: []domain.ReportFacts{report(year, 0, 0), report(year, 3, 2), report(day*2, 0, 4)},
			want:    domain.VerdictUnclear,
		},
		{name: "no reports, nothing to judge", reports: nil, want: domain.VerdictUnclear},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := Reports(tc.reports, DefaultReportRules)
			if v.Kind != tc.want {
				t.Fatalf("kind = %s, want %s (evidence %q)", v.Kind, tc.want, v.Evidence)
			}
			if tc.evidence != nil && !slices.Equal(v.Evidence, tc.evidence) {
				t.Fatalf("evidence = %q\nwant      %q", v.Evidence, tc.evidence)
			}
			if v.Explanation == "" {
				t.Fatal("a verdict always explains itself")
			}
		})
	}
}

func TestPlural(t *testing.T) {
	for n, want := range map[int]string{1: "скарга", 2: "скарги", 4: "скарги", 5: "скарг", 11: "скарг", 12: "скарг", 21: "скарга", 22: "скарги"} {
		if got := plural(n, "скарга", "скарги", "скарг"); got != want {
			t.Errorf("plural(%d) = %s, want %s", n, got, want)
		}
	}
}
