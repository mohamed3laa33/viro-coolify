package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
)

// StripeProvider talks to the Stripe REST API directly (no SDK dependency).
// It is used only when billing is enabled and a secret key is configured.
type StripeProvider struct {
	secretKey  string
	successURL string
	cancelURL  string
	httpClient *http.Client
}

// NewStripeProvider builds a Stripe-backed payment provider.
func NewStripeProvider(secretKey, successURL, cancelURL string) *StripeProvider {
	return &StripeProvider{
		secretKey:  secretKey,
		successURL: successURL,
		cancelURL:  cancelURL,
		httpClient: &http.Client{Timeout: 20 * time.Second},
	}
}

func (s *StripeProvider) Name() string { return "stripe" }

func (s *StripeProvider) post(ctx context.Context, path string, form url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.stripe.com/v1"+path, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.secretKey)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("stripe: status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

// EnsureCustomer creates a Stripe customer for the org.
func (s *StripeProvider) EnsureCustomer(ctx context.Context, orgID, email string) (string, error) {
	form := url.Values{}
	form.Set("email", email)
	form.Set("metadata[org_id]", orgID)
	var out struct {
		ID string `json:"id"`
	}
	if err := s.post(ctx, "/customers", form, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// CreateSubscription creates a Checkout Session for the plan and returns its URL.
func (s *StripeProvider) CreateSubscription(ctx context.Context, customerID string, plan domain.Plan) (ProviderSubscription, error) {
	if plan.StripePriceID == "" {
		return ProviderSubscription{}, errors.New("stripe: plan has no StripePriceID configured")
	}
	form := url.Values{}
	form.Set("mode", "subscription")
	form.Set("customer", customerID)
	form.Set("line_items[0][price]", plan.StripePriceID)
	form.Set("line_items[0][quantity]", "1")
	form.Set("success_url", s.successURL)
	form.Set("cancel_url", s.cancelURL)
	var out struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	if err := s.post(ctx, "/checkout/sessions", form, &out); err != nil {
		return ProviderSubscription{}, err
	}
	return ProviderSubscription{ID: out.ID, Status: string(domain.SubIncomplete), CheckoutURL: out.URL}, nil
}
