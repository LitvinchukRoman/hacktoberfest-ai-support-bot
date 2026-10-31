// Package billing is the bot's client for fantasm's support billing API
// (/api/integrations/support/billing). It reads what the person pays for and can
// give free days of their plan, and nothing else: cancelling a plan or refunding a
// payment is a support manager's call, so this client has no way to ask for either.
//
// The bot never names a user. It forwards the short-lived token fantasm issued to
// the person's browser, next to its own SUPPORT_API_TOKEN, and fantasm resolves both.
package billing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/handziurdmytro/skelar/internal/support/domain"
)

// UserHeader carries the person's identity token to fantasm.
const UserHeader = "X-Fantasm-Support-User"

// ErrRejected means fantasm did not accept the identity token: it expired or was forged.
var ErrRejected = errors.New("fantasm rejected the support identity")

const (
	requestTimeout = 5 * time.Second
	catalogTTL     = 10 * time.Minute
	maxBody        = 1 << 20
)

type Client struct {
	base     string
	apiToken string
	http     *http.Client
	now      func() time.Time

	mu        sync.Mutex
	catalog   catalogDTO
	catalogAt time.Time
}

// New returns a client for fantasm at baseURL (scheme and host, no path).
func New(baseURL, apiToken string) *Client {
	return &Client{
		base:     strings.TrimRight(baseURL, "/"),
		apiToken: apiToken,
		http:     &http.Client{Timeout: requestTimeout},
		now:      time.Now,
	}
}

type planDTO struct {
	Code        string `json:"code"`
	Name        string `json:"name"`
	PriceMinor  int    `json:"priceMinor"`
	ActiveIdeas *int   `json:"activeIdeas"`
}

type tierDTO struct {
	Code       string `json:"code"`
	Name       string `json:"name"`
	PriceMinor int    `json:"priceMinor"`
}

type catalogDTO struct {
	Plans []planDTO `json:"plans"`
	Tiers []tierDTO `json:"tiers"`
}

type snapshotDTO struct {
	User struct {
		Handle string `json:"handle"`
	} `json:"user"`
	Plan struct {
		planDTO
		CurrentPeriodEnd  *time.Time `json:"currentPeriodEnd"`
		CancelAtPeriodEnd bool       `json:"cancelAtPeriodEnd"`
	} `json:"plan"`
	Usage struct {
		ActiveIdeas int `json:"activeIdeas"`
	} `json:"usage"`
	Sponsorships []struct {
		ID   string `json:"id"`
		Idea struct {
			Slug  string `json:"slug"`
			Title string `json:"title"`
		} `json:"idea"`
		Tier              string    `json:"tier"`
		TierName          string    `json:"tierName"`
		PriceMinor        int       `json:"priceMinor"`
		CurrentPeriodEnd  time.Time `json:"currentPeriodEnd"`
		CancelAtPeriodEnd bool      `json:"cancelAtPeriodEnd"`
	} `json:"sponsorships"`
	Payments []struct {
		ID          string    `json:"id"`
		Kind        string    `json:"kind"`
		AmountMinor int       `json:"amountMinor"`
		Status      string    `json:"status"`
		Description string    `json:"description"`
		CreatedAt   time.Time `json:"createdAt"`
	} `json:"payments"`
	Replayed bool `json:"replayed"`
}

func (p planDTO) facts() domain.PlanFacts {
	out := domain.PlanFacts{Code: p.Code, Name: p.Name, PriceMinor: p.PriceMinor}
	if p.ActiveIdeas != nil {
		out.ActiveIdeas = *p.ActiveIdeas
	}
	return out
}

// Billing returns what the person behind identity pays for. An empty identity
// means a visitor: there is nothing to look up, so it returns nil without asking.
func (c *Client) Billing(ctx context.Context, identity string) (*domain.BillingFacts, error) {
	if identity == "" {
		return nil, nil
	}
	catalog, err := c.prices(ctx)
	if err != nil {
		return nil, err
	}
	var s snapshotDTO
	if err := c.do(ctx, http.MethodGet, "/api/integrations/support/billing", identity, nil, &s); err != nil {
		return nil, err
	}
	return toFacts(s, catalog), nil
}

// ExtendPlan gives the person days of their paid plan for free and returns the new
// end of the paid period. actionID is the idempotency key: fantasm applies it once
// and answers a replay with the current period and replayed=true.
func (c *Client) ExtendPlan(ctx context.Context, identity, actionID string, days int) (domain.Extension, error) {
	if identity == "" {
		return domain.Extension{}, ErrRejected
	}
	body := map[string]any{"actionId": actionID, "action": "EXTEND_PLAN", "days": days}
	var s snapshotDTO
	if err := c.do(ctx, http.MethodPost, "/api/integrations/support/billing/actions", identity, body, &s); err != nil {
		return domain.Extension{}, err
	}
	out := domain.Extension{Replayed: s.Replayed}
	if s.Plan.CurrentPeriodEnd != nil {
		out.PeriodEnd = *s.Plan.CurrentPeriodEnd
	}
	return out, nil
}

var unsafeActionID = regexp.MustCompile(`[^A-Za-z0-9_.:-]`)

// GoodwillActionID is the idempotency key of the one gift of free days a person gets.
func GoodwillActionID(handle string) string {
	id := "support-goodwill:" + unsafeActionID.ReplaceAllString(handle, "_")
	if len(id) > 100 {
		id = id[:100]
	}
	return id
}

func (c *Client) prices(ctx context.Context) (catalogDTO, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.catalogAt.IsZero() && c.now().Sub(c.catalogAt) < catalogTTL {
		return c.catalog, nil
	}
	var cat catalogDTO
	if err := c.do(ctx, http.MethodGet, "/api/billing/plans", "", nil, &cat); err != nil {
		return catalogDTO{}, fmt.Errorf("load price list: %w", err)
	}
	c.catalog, c.catalogAt = cat, c.now()
	return cat, nil
}

func (c *Client) do(ctx context.Context, method, path, identity string, body, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if identity != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiToken)
		req.Header.Set(UserHeader, identity)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("fantasm %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return fmt.Errorf("fantasm %s %s: read body: %w", method, path, err)
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return ErrRejected
	case resp.StatusCode >= 300:
		// The body is fantasm's error envelope; it never carries the token, but keep it short.
		return fmt.Errorf("fantasm %s %s: status %d: %.200s", method, path, resp.StatusCode, raw)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("fantasm %s %s: decode: %w", method, path, err)
	}
	return nil
}

func toFacts(s snapshotDTO, cat catalogDTO) *domain.BillingFacts {
	f := &domain.BillingFacts{
		Handle:            s.User.Handle,
		Plan:              s.Plan.facts(),
		CancelAtPeriodEnd: s.Plan.CancelAtPeriodEnd,
		ActiveIdeas:       s.Usage.ActiveIdeas,
	}
	if s.Plan.CurrentPeriodEnd != nil {
		f.PeriodEnd = *s.Plan.CurrentPeriodEnd
	}
	for _, p := range cat.Plans {
		f.Plans = append(f.Plans, p.facts())
	}
	for _, t := range cat.Tiers {
		f.Tiers = append(f.Tiers, domain.TierFacts{Code: t.Code, Name: t.Name, PriceMinor: t.PriceMinor})
	}
	slices.SortStableFunc(f.Plans, func(a, b domain.PlanFacts) int { return a.PriceMinor - b.PriceMinor })
	slices.SortStableFunc(f.Tiers, func(a, b domain.TierFacts) int { return a.PriceMinor - b.PriceMinor })
	for _, sp := range s.Sponsorships {
		f.Sponsorships = append(f.Sponsorships, domain.SponsorshipFacts{
			ID: sp.ID, IdeaSlug: sp.Idea.Slug, IdeaTitle: sp.Idea.Title, Tier: sp.Tier, TierName: sp.TierName,
			PriceMinor: sp.PriceMinor, PeriodEnd: sp.CurrentPeriodEnd, CancelAtPeriodEnd: sp.CancelAtPeriodEnd,
		})
	}
	for _, p := range s.Payments {
		f.Payments = append(f.Payments, domain.PaymentFacts{
			ID: p.ID, Kind: p.Kind, AmountMinor: p.AmountMinor, Status: p.Status, Description: p.Description, CreatedAt: p.CreatedAt,
		})
	}
	return f
}
