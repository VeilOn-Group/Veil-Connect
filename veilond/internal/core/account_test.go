package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/veilon/veil-shared/pkg/release"
	"github.com/veilon/veil-shared/pkg/sign"
)

// accountAPI extends fakeAPI with the optional data-rights and release calls.
type accountAPI struct {
	*fakeAPI
	rel      *sign.Signer
	relEnv   *sign.Envelope
	deleted  bool
	exported bool
}

func (a *accountAPI) Keyset(context.Context) (*sign.Envelope, error) {
	return a.root.Sign(sign.Keyset{Version: 1, Keys: []sign.PublicKey{
		{ID: a.prof.KeyID, Purpose: sign.PurposeProfile, Key: a.prof.PublicKey()},
		{ID: a.rel.KeyID, Purpose: sign.PurposeRelease, Key: a.rel.PublicKey()},
	}})
}
func (a *accountAPI) ExportAccount(context.Context, string) (json.RawMessage, error) {
	a.exported = true
	return json.RawMessage(`{"account":{"id":"x"}}`), nil
}
func (a *accountAPI) DeleteAccount(context.Context, string) error { a.deleted = true; return nil }
func (a *accountAPI) LatestRelease(context.Context, string, string) (*sign.Envelope, error) {
	return a.relEnv, nil
}

func TestUpdateVerifyAndDownload(t *testing.T) {
	base := newFakeAPI(t)
	seed, _ := sign.GenerateSeed()
	rel, _ := sign.NewSigner("rel-1", sign.PurposeRelease, seed)
	api := &accountAPI{fakeAPI: base, rel: rel}
	payload := []byte("pretend installer bytes")
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(payload) }))
	defer srv.Close()
	sum := sha256.Sum256(payload)
	meta := release.Metadata{Platform: Platform(), Channel: "stable", Version: "0.9.0", URL: srv.URL + "/VeilOn-0.9.0.bin",
		SHA256: hex.EncodeToString(sum[:]), Size: int64(len(payload)), IssuedAt: testNow.Unix()}
	api.relEnv, _ = rel.Sign(meta)

	m, _, _ := newManager(t, base)
	m.api = api
	m.opt.UpdateHTTP = srv.Client()
	ctx := context.Background()
	if _, err := m.Register(ctx); err != nil {
		t.Fatal(err)
	}
	info, err := m.CheckUpdate(ctx, "")
	if err != nil || !info.Available || info.Version != "0.9.0" {
		t.Fatalf("check: %+v %v", info, err)
	}
	got, err := m.DownloadUpdate(ctx)
	if err != nil || !got.Downloaded {
		t.Fatalf("download: %v", err)
	}
	b, _ := os.ReadFile(got.Path)
	if string(b) != string(payload) {
		t.Fatal("artifact content")
	}

	// Tampered artifact (hash mismatch) is discarded.
	payload = []byte("evil bytes of the same length!!")
	if _, err := m.DownloadUpdate(ctx); !errors.Is(err, ErrUpdateHash) {
		t.Fatalf("tampered download: %v", err)
	}

	// Metadata signed by a key outside the keyset is rejected.
	seed2, _ := sign.GenerateSeed()
	evil, _ := sign.NewSigner("rel-1", sign.PurposeRelease, seed2)
	api.relEnv, _ = evil.Sign(meta)
	if _, err := m.CheckUpdate(ctx, ""); err == nil {
		t.Fatal("forged release accepted")
	}
	// Downgrade is never offered.
	meta.Version = "0.0.9"
	api.relEnv, _ = rel.Sign(meta)
	if info, err := m.CheckUpdate(ctx, ""); err != nil || info.Available {
		t.Fatalf("downgrade offered: %+v %v", info, err)
	}
}

func TestExportDelete(t *testing.T) {
	base := newFakeAPI(t)
	seed, _ := sign.GenerateSeed()
	rel, _ := sign.NewSigner("rel-1", sign.PurposeRelease, seed)
	api := &accountAPI{fakeAPI: base, rel: rel}
	m, _, _ := newManager(t, base)
	m.api = api
	ctx := context.Background()
	if _, err := m.Register(ctx); err != nil {
		t.Fatal(err)
	}
	if raw, err := m.ExportAccount(ctx); err != nil || !api.exported || len(raw) == 0 {
		t.Fatalf("export: %v", err)
	}
	if err := m.DeleteAccount(ctx); err != nil || !api.deleted {
		t.Fatalf("delete: %v", err)
	}
	if _, err := m.store.LoadTokens(); err == nil {
		t.Fatal("local session must be wiped after deletion")
	}
}

// TestInstallUpdateSubmitsToHelper: veilond only hands its own verified
// download to the helper's inbox (ADR-0069).
func TestInstallUpdateSubmitsToHelper(t *testing.T) {
	base := newFakeAPI(t)
	seed, _ := sign.GenerateSeed()
	rel, _ := sign.NewSigner("rel-1", sign.PurposeRelease, seed)
	api := &accountAPI{fakeAPI: base, rel: rel}
	payload := []byte("pretend installer bytes")
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(payload) }))
	defer srv.Close()
	sum := sha256.Sum256(payload)
	meta := release.Metadata{Platform: Platform(), Channel: "stable", Version: "0.9.0", URL: srv.URL + "/VeilOn-0.9.0.bin",
		SHA256: hex.EncodeToString(sum[:]), Size: int64(len(payload)), IssuedAt: testNow.Unix()}
	api.relEnv, _ = rel.Sign(meta)
	m, _, _ := newManager(t, base)
	m.api = api
	m.opt.UpdateHTTP = srv.Client()
	ctx := context.Background()
	if _, err := m.Register(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := m.InstallUpdate(ctx); !errors.Is(err, ErrNoSilentInstall) {
		t.Fatalf("no helper configured: %v", err)
	}
	m.opt.UpdateInbox = t.TempDir()
	if _, err := m.InstallUpdate(ctx); err == nil {
		t.Fatal("install without a verified download accepted")
	}
	if info, err := m.CheckUpdate(ctx, ""); err != nil || !info.SilentInstall {
		t.Fatalf("check: %+v %v", info, err)
	}
	if _, err := m.DownloadUpdate(ctx); err != nil {
		t.Fatal(err)
	}
	info, err := m.InstallUpdate(ctx)
	if err != nil || !info.Installing {
		t.Fatalf("install: %+v %v", info, err)
	}
	for _, f := range []string{"keyset.json", "release.json", "artifact", "request"} {
		if _, err := os.Stat(m.opt.UpdateInbox + "/" + f); err != nil {
			t.Fatalf("inbox %s: %v", f, err)
		}
	}
	b, _ := os.ReadFile(m.opt.UpdateInbox + "/artifact")
	if string(b) != string(payload) {
		t.Fatal("artifact")
	}
}
