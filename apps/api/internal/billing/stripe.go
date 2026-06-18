package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
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
//
// It attaches the org id as metadata on the SUBSCRIPTION the checkout will create
// (subscription_data[metadata][org_id]) and as client_reference_id on the session,
// so the checkout.session.completed and customer.subscription.* webhooks can map
// the event back to the org. The returned ProviderSubscription has an EMPTY ID:
// the real sub_ id does not exist until checkout completes and is captured by the
// webhook — the checkout-session (cs_) id is deliberately not surfaced as the
// subscription id.
func (s *StripeProvider) CreateSubscription(ctx context.Context, orgID, customerID string, plan domain.Plan) (ProviderSubscription, error) {
	if plan.StripePriceID == "" {
		return ProviderSubscription{}, errors.New("stripe: plan has no StripePriceID configured")
	}
	form := url.Values{}
	form.Set("mode", "subscription")
	form.Set("customer", customerID)
	form.Set("client_reference_id", orgID)
	form.Set("line_items[0][price]", plan.StripePriceID)
	form.Set("line_items[0][quantity]", "1")
	// Stamp org_id onto the subscription Stripe creates on completion, so every
	// customer.subscription.* event carries it in data.object.metadata.org_id.
	form.Set("subscription_data[metadata][org_id]", orgID)
	form.Set("success_url", s.successURL)
	form.Set("cancel_url", s.cancelURL)
	var out struct {
		URL string `json:"url"`
	}
	if err := s.post(ctx, "/checkout/sessions", form, &out); err != nil {
		return ProviderSubscription{}, err
	}
	return ProviderSubscription{ID: "", Status: string(domain.SubIncomplete), CheckoutURL: out.URL}, nil
}

// ReportUsage records metered usage against the subscription's metered price item.
// It posts a usage record to Stripe so the metered compute-hours roll into the
// customer's invoice. quantity is the whole compute-hours to add. This satisfies
// billing.UsageReporter; the MockProvider does not implement it (no-op).
func (s *StripeProvider) ReportUsage(ctx context.Context, subscriptionItemID string, quantity int64, at time.Time) error {
	if subscriptionItemID == "" || quantity <= 0 {
		return nil
	}
	form := url.Values{}
	form.Set("quantity", strconv.FormatInt(quantity, 10))
	form.Set("timestamp", strconv.FormatInt(at.UTC().Unix(), 10))
	form.Set("action", "increment")
	return s.post(ctx, "/subscription_items/"+url.PathEscape(subscriptionItemID)+"/usage_records", form, nil)
}
