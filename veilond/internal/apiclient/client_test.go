package apiclient

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/veilon/veil-shared/pkg/apitypes"
)

func tlsServer(t *testing.T, h http.HandlerFunc) (*httptest.Server, string) {
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	return srv, SPKIPin(srv.Certificate())
}

func TestPinningAcceptsMatchingKey(t *testing.T) {
	srv, pin := tlsServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(apitypes.MeResponse{Status: "active"})
	})
	c, err := New(Options{BaseURLs: []string{srv.URL}, SPKIPins: []string{pin}})
	if err != nil {
		t.Fatal(err)
	}
	// The test server cert is self-signed; pinning replaces CA trust only via
	// the custom root pool below.
	c.hc.Transport.(*http.Transport).TLSClientConfig.RootCAs = srv.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	me, err := c.Me(context.Background(), "tok")
	if err != nil || me.Status != "active" {
		t.Fatalf("me %+v err %v", me, err)
	}
}

func TestPinningRejectsOtherKey(t *testing.T) {
	srv, _ := tlsServer(t, func(w http.ResponseWriter, r *http.Request) {})
	// httptest servers share one built-in certificate, so derive the "wrong"
	// pin from a freshly generated key instead.
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.MarshalPKIXPublicKey(&k.PublicKey)
	sum := sha256.Sum256(der)
	otherPin := base64.StdEncoding.EncodeToString(sum[:])
	c, _ := New(Options{BaseURLs: []string{srv.URL}, SPKIPins: []string{otherPin}})
	c.hc.Transport.(*http.Transport).TLSClientConfig.RootCAs = srv.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	_, err := c.Me(context.Background(), "tok")
	if !errors.Is(err, ErrPinMismatch) {
		t.Fatalf("want pin mismatch, got %v", err)
	}
}

func TestRequiresPins(t *testing.T) {
	if _, err := New(Options{BaseURLs: []string{"https://x"}}); err == nil {
		t.Fatal("pins must be required outside dev")
	}
}

func TestEndpointRotationAndProof(t *testing.T) {
	var nonce = "n-123"
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/auth/challenge":
			_ = json.NewEncoder(w).Encode(apitypes.ChallengeResponse{Nonce: nonce, ExpiresIn: 60})
		case "/v1/auth/register":
			var req apitypes.RegisterRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			pub, err := apitypes.DecodePublicKey(req.DevicePublicKey)
			if err != nil || req.Nonce != nonce || !apitypes.VerifyDeviceSignature(pub, req.Nonce, req.Signature) {
				w.WriteHeader(401)
				_ = json.NewEncoder(w).Encode(apitypes.Error{Code: apitypes.CodeInvalidProof})
				return
			}
			if r.Header.Get("User-Agent") != "VeilOn" {
				w.WriteHeader(400)
				return
			}
			_ = json.NewEncoder(w).Encode(apitypes.TokenResponse{AccessToken: "a", RefreshToken: "r", AccountNumber: "1111222233334444"})
		}
	}))
	defer good.Close()
	// First endpoint is dead (closed port) -> rotate to the second.
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()
	c, _ := New(Options{BaseURLs: []string{deadURL, good.URL}, AllowUnpinned: true, Timeout: 5 * time.Second})
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	tok, err := c.Register(context.Background(), key, "")
	if err != nil || tok.AccountNumber == "" {
		t.Fatalf("register: %+v %v", tok, err)
	}
	if c.pref != 1 {
		t.Fatalf("preferred endpoint not updated: %d", c.pref)
	}
}
