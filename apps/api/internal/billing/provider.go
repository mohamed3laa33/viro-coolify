package billing

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
)

// ErrInvalidSignature is returned when a webhook signature fails verification.
var ErrInvalidSignature = errors.New("billing: invalid webhook signature")

// ProviderSubscription is the payment provider's view of a created subscription.
type ProviderSubscription struct {
	// ID is the real subscription id (sub_…) when the provider activates inline
	// (MockProvider), or empty for a Stripe Checkout flow where the sub_ id only
	// exists after checkout.session.completed (captured by the webhook). The
	// checkout-session id (cs_…) is NEVER stored as the subscription id.
	ID          string
	Status      string
	CheckoutURL string // non-empty when the customer must complete checkout
}

// PaymentProvider abstracts the payment backend so the service is testable and
// Stripe is optional. Implementations: MockProvider (default), StripeProvider.
//
// CreateSubscription receives the orgID so the provider can attach it as metadata
// on BOTH the Stripe customer and the subscription (subscription_data[metadata]
// [org_id]); this is what lets a later customer.subscription.* webhook map the
// event back to the org.
type PaymentProvider interface {
	Name() string
	EnsureCustomer(ctx context.Context, orgID, email string) (customerID string, err error)
	CreateSubscription(ctx context.Context, orgID, customerID string, plan domain.Plan) (ProviderSubscription, error)
}

// MockProvider activates subscriptions immediately with deterministic IDs and no
// external calls. It is the default in local/dev and the provider used in tests.
type MockProvider struct{}

func (MockProvider) Name() string { return "mock" }

func (MockProvider) EnsureCustomer(_ context.Context, orgID, _ string) (string, error) {
	return "cus_mock_" + orgID, nil
}

func (MockProvider) CreateSubscription(_ context.Context, orgID, _ string, plan domain.Plan) (ProviderSubscription, error) {
	// The mock activates inline with a real (deterministic) sub_ id so the local/
	// dev billing UX works end-to-end without Stripe or a webhook round-trip.
	return ProviderSubscription{ID: "sub_mock_" + orgID + "_" + plan.ID, Status: string(domain.SubActive)}, nil
}

// VerifyWebhookSignature validates a Stripe-style `Stripe-Signature` header of
// the form `t=<unix>,v1=<hex-hmac>[,v1=...]`. The signed payload is `t.body`,
// HMAC-SHA256 with the endpoint secret. A non-zero tolerance rejects stale events.
func VerifyWebhookSignature(payload []byte, sigHeader, secret string, tolerance time.Duration, now time.Time) error {
	if secret == "" {
		return ErrInvalidSignature
	}
	var ts string
	var sigs []string
	for _, part := range strings.Split(sigHeader, ",") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		switch kv[0] {
		case "t":
			ts = kv[1]
		case "v1":
			sigs = append(sigs, kv[1])
		}
	}
	if ts == "" || len(sigs) == 0 {
		return ErrInvalidSignature
	}
	tsInt, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return ErrInvalidSignature
	}
	if tolerance > 0 {
		diff := now.Sub(time.Unix(tsInt, 0))
		if diff < 0 {
			diff = -diff
		}
		if diff > tolerance {
			return ErrInvalidSignature
		}
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "."))
	mac.Write(payload)
	expected := hex.EncodeToString(mac.Sum(nil))
	for _, s := range sigs {
		if hmac.Equal([]byte(expected), []byte(s)) {
			return nil
		}
	}
	return ErrInvalidSignature
}
