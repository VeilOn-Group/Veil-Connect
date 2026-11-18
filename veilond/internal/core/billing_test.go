package core

import (
	"context"
	"errors"
	"testing"

	"github.com/veilon/veil-shared/pkg/apitypes"
)

// billAPI extends fakeAPI with the payment calls.
type billAPI struct {
	*fakeAPI
	url    string
	status string
	access string
}

func (b *billAPI) Offers(context.Context) ([]apitypes.BillingOffer, error) {
	return []apitypes.BillingOffer{{PlanID: "pro", Prices: []apitypes.BillingPrice{{Provider: "yookassa", AmountMinor: 49900, Currency: "RUB"}}}}, nil
}

func (b *billAPI) Checkout(_ context.Context, access, plan, provider string) (*apitypes.CheckoutResponse, error) {
	b.access = access
	return &apitypes.CheckoutResponse{OrderID: "o-1", Provider: provider, AmountMinor: 49900, Currency: "RUB", ConfirmationURL: b.url}, nil
}

func (b *billAPI) Order(_ context.Context, _, id string) (*apitypes.BillingOrder, error) {
	return &apitypes.BillingOrder{ID: id, Status: b.status}, nil
}

func TestPaymentURLAllowed(t *testing.T) {
	for u, want := range map[string]bool{
		"https://yoomoney.ru/checkout/payments/v2/contract?orderId=1": true,
		"https://pay.heleket.com/pay/4b605bbd":                        true,
		"https://sub.yookassa.ru/x":                                   true,
		"http://yoomoney.ru/x":                                        false,
		"https://yoomoney.ru.evil.example/x":                          false,
		"https://evilyoomoney.ru/x":                                   false,
		"https://user@yoomoney.ru/x":                                  false,
		"https://yoomoney.ru:8443/x":                                  false,
		"https://heleket.com/pay/x":                                   false,
		"javascript:alert(1)":                                         false,
		"":                                                            false,
	} {
		if got := PaymentURLAllowed(u, DefaultPaymentHosts); got != want {
			t.Errorf("%q: %v", u, got)
		}
	}
}

func TestCheckoutFlow(t *testing.T) {
	base := newFakeAPI(t)
	api := &billAPI{fakeAPI: base, url: "https://yoomoney.ru/checkout/payments/v2/contract?orderId=1", status: "pending"}
	m, _, _ := newManager(t, base)
	m.api = api
	ctx := context.Background()
	if _, err := m.Checkout(ctx, "pro", "yookassa"); !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("checkout before login: %v", err)
	}
	if _, err := m.Register(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := m.PaymentURL(); !errors.Is(err, ErrNoCheckout) {
		t.Fatal("payment url without checkout")
	}
	if _, err := m.Checkout(ctx, "", "yookassa"); !errors.Is(err, ErrBadCheckoutArg) {
		t.Fatal("empty plan accepted")
	}
	co, err := m.Checkout(ctx, "pro", "yookassa")
	if err != nil || co.OrderID != "o-1" || api.access == "" {
		t.Fatalf("checkout %+v %v", co, err)
	}
	if u, _ := m.PaymentURL(); u != api.url {
		t.Fatal(u)
	}
	if o, err := m.CurrentOrder(ctx); err != nil || o.Status != "pending" {
		t.Fatalf("order %+v %v", o, err)
	}
	api.status = apitypes.OrderFulfilled
	if o, err := m.CurrentOrder(ctx); err != nil || o.Status != apitypes.OrderFulfilled {
		t.Fatalf("order %+v %v", o, err)
	}
	if _, err := m.CurrentOrder(ctx); !errors.Is(err, ErrNoCheckout) {
		t.Fatal("fulfilled order not forgotten")
	}

	// A confirmation URL outside the provider hosts is never handed out.
	api.url = "https://phish.example/pay"
	if _, err := m.Checkout(ctx, "pro", "yookassa"); !errors.Is(err, ErrPaymentURL) {
		t.Fatalf("foreign url: %v", err)
	}
	if _, err := m.PaymentURL(); !errors.Is(err, ErrNoCheckout) {
		t.Fatal("foreign url remembered")
	}
}
