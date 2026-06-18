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
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/retryx"
)

// StripeProvider talks to the Stripe REST API directly (no SDK dependency).
// It is used only when billing is enabled and a secret key is configured.
type StripeProvider struct {
	secretKey  string
	successURL string
	cancelURL  string
	httpClient *http.Client
	// baseURL is the Stripe API root; overridable in tests (default the live API).
	baseURL string
	// retry bounds the per-request retry-with-backoff for TRANSIENT failures
	// (network errors and 5xx); a 4xx is terminal and never retried.
	retry retryx.Policy
}

// NewStripeProvider builds a Stripe-backed payment provider.
func NewStripeProvider(secretKey, successURL, cancelURL string) *StripeProvider {
	return &StripeProvider{
		secretKey:  secretKey,
		successURL: successURL,
		cancelURL:  cancelURL,
		httpClient: &http.Client{Timeout: 20 * time.Second},
		baseURL:    "https://api.stripe.com/v1",
		retry:      retryx.DefaultPolicy(),
	}
}

func (s *StripeProvider) Name() string { return "stripe" }

// apiBase returns the Stripe API root, defaulting to the live API when unset (so a
// zero-value provider built outside the constructor still targets Stripe).
func (s *StripeProvider) apiBase() string {
	if s.baseURL == "" {
		return "https://api.stripe.com/v1"
	}
	return s.baseURL
}

// post issues a Stripe API call with a bounded retry-with-backoff. A network
// error or a 5xx response is TRANSIENT and retried (with exponential backoff +
// jitter from s.retry, honoring ctx cancellation); a 4xx response is a TERMINAL
// client error (bad request / auth / not-found) and is returned immediately
// without further attempts. Stripe writes are idempotent-safe to retry on a
// transient failure (a failed request either did not reach Stripe or did not
// complete); for stricter guarantees an Idempotency-Key could be added later.
func (s *StripeProvider) post(ctx context.Context, path string, form url.Values, out any) error {
	body := form.Encode()
	return retryx.Do(ctx, s.retry, func(ctx context.Context) error {
		return s.postOnce(ctx, path, body, out)
	})
}

// postOnce performs a single Stripe round-trip. It returns nil on success, a
// retryx.Terminal-wrapped error for a non-retryable failure (4xx, an unparseable
// success body, a request-build error), or a bare error for a TRANSIENT failure
// (network error, 5xx) so retryx backs off and retries.
func (s *StripeProvider) postOnce(ctx context.Context, path, body string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.apiBase()+path, strings.NewReader(body))
	if err != nil {
		// A malformed request is not going to fix itself on retry.
		return retryx.Terminal(err)
	}
	req.Header.Set("Authorization", "Bearer "+s.secretKey)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		// A context cancellation/deadline is terminal (don't keep retrying a
		// cancelled call); any other transport error is transient and retryable.
		if ctx.Err() != nil {
			return retryx.Terminal(err)
		}
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		statusErr := fmt.Errorf("stripe: status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
		// 5xx and 429 (rate-limit) are transient; 4xx (other than 429) is terminal.
		if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
			return statusErr
		}
		return retryx.Terminal(statusErr)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return retryx.Terminal(err)
		}
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
// It posts a usage record to Stripe so the metered compute COST rolls into the
// customer's invoice.
//
// UNIT: quantity is the whole CENTS of size-aware metered compute cost to add (the
// same unit the caller, Service.ReportUsage, computes and the unit stored end to
// end — see MeterMetric / usageSoFarCents). It is NOT compute-hours: the platform
// meters cost, not raw hours, so a 64-vCPU workload accrues 64x the cents of a
// 1-vCPU workload for the same wall-clock time. The Stripe metered price item this
// reports against MUST therefore be configured at 1 unit = 1 cent (i.e. a
// unit_amount of $0.01 / 1 cent per unit), so the reported quantity equals the
// invoiced amount in cents. This satisfies billing.UsageReporter; the MockProvider
// does not implement it (no-op).
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
