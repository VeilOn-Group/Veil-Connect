package core

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"sync"

	"github.com/veilon/veil-shared/pkg/apitypes"
)

// BillingAPI is the payment part of the server API (apiclient.Client).
type BillingAPI interface {
	Offers(ctx context.Context) ([]apitypes.BillingOffer, error)
	Checkout(ctx context.Context, access, planID, provider string) (*apitypes.CheckoutResponse, error)
	Order(ctx context.Context, access, id string) (*apitypes.BillingOrder, error)
}

// DefaultPaymentHosts are the hosts a confirmation URL may point to: the
// YooKassa checkout (yoomoney.ru) [ПРОВЕРИТЬ against a live payment] and
// the Heleket hosted page (pay.heleket.com, from doc.heleket.com).
// A subdomain of a listed host matches.
var DefaultPaymentHosts = []string{"yoomoney.ru", "yookassa.ru", "pay.heleket.com"}

// Payment errors.
var (
	ErrNoCheckout     = errors.New("core: no payment in progress")
	ErrPaymentURL     = errors.New("core: payment page address is not a known provider page")
	ErrBadCheckoutArg = errors.New("core: plan_id and provider are required")
)

// payState is the payment in progress. It lives in memory only: nothing
// about purchases is written to disk on the device.
type payState struct {
	mu sync.Mutex
	co *apitypes.CheckoutResponse
}

func (m *Manager) billingAPI() (BillingAPI, error) {
	b, ok := m.api.(BillingAPI)
	if !ok {
		return nil, ErrUnsupported
	}
	return b, nil
}

// Offers returns the plans for sale.
func (m *Manager) Offers(ctx context.Context) ([]apitypes.BillingOffer, error) {
	b, err := m.billingAPI()
	if err != nil {
		return nil, err
	}
	return b.Offers(ctx)
}

// Checkout creates a payment and remembers it as the one in progress. The
// confirmation URL must point to a known provider page: the UI process
// opens it in the system browser (ADR-0023).
func (m *Manager) Checkout(ctx context.Context, planID, provider string) (*apitypes.CheckoutResponse, error) {
	if planID == "" || provider == "" || len(planID) > 64 || len(provider) > 32 {
		return nil, ErrBadCheckoutArg
	}
	b, err := m.billingAPI()
	if err != nil {
		return nil, err
	}
	access, err := m.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	co, err := b.Checkout(ctx, access, planID, provider)
	if err != nil {
		return nil, err
	}
	if !PaymentURLAllowed(co.ConfirmationURL, m.paymentHosts()) {
		return nil, ErrPaymentURL
	}
	m.pay.mu.Lock()
	m.pay.co = co
	m.pay.mu.Unlock()
	return co, nil
}

// PaymentURL is the confirmation URL of the payment in progress.
func (m *Manager) PaymentURL() (string, error) {
	m.pay.mu.Lock()
	defer m.pay.mu.Unlock()
	if m.pay.co == nil {
		return "", ErrNoCheckout
	}
	return m.pay.co.ConfirmationURL, nil
}

// CurrentOrder polls the order in progress. Once it is fulfilled the
// profile is refreshed (new plan, new end date) and the payment forgotten;
// an expired or canceled order is forgotten too.
func (m *Manager) CurrentOrder(ctx context.Context) (*apitypes.BillingOrder, error) {
	m.pay.mu.Lock()
	co := m.pay.co
	m.pay.mu.Unlock()
	if co == nil {
		return nil, ErrNoCheckout
	}
	b, err := m.billingAPI()
	if err != nil {
		return nil, err
	}
	access, err := m.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	o, err := b.Order(ctx, access, co.OrderID)
	if err != nil {
		return nil, err
	}
	switch o.Status {
	case apitypes.OrderFulfilled, apitypes.OrderExpired, apitypes.OrderCanceled:
		m.pay.mu.Lock()
		if m.pay.co == co {
			m.pay.co = nil
		}
		m.pay.mu.Unlock()
		if o.Status == apitypes.OrderFulfilled {
			if err := m.RefreshProfile(ctx); err != nil {
				m.opt.Logger.Warn("profile refresh after payment", "err", err)
			}
		}
	}
	return o, nil
}

func (m *Manager) paymentHosts() []string {
	if len(m.opt.PaymentHosts) > 0 {
		return m.opt.PaymentHosts
	}
	return DefaultPaymentHosts
}

// PaymentURLAllowed reports whether u is an https URL on one of hosts (or
// a subdomain), with no credentials and the default port.
func PaymentURLAllowed(u string, hosts []string) bool {
	p, err := url.Parse(u)
	if err != nil || p.Scheme != "https" || p.User != nil || p.Port() != "" || p.Opaque != "" {
		return false
	}
	h := strings.ToLower(p.Hostname())
	for _, a := range hosts {
		a = strings.ToLower(a)
		if h == a || strings.HasSuffix(h, "."+a) {
			return true
		}
	}
	return false
}
