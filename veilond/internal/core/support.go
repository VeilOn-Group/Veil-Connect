package core

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
)

// SupportAPI is the support part of the server API (apiclient.Client).
type SupportAPI interface {
	SupportGet(ctx context.Context, access, path string) (json.RawMessage, error)
	SupportPost(ctx context.Context, access, path string, in any) (json.RawMessage, error)
}

// ErrSupportPath is returned for a path outside the support API shape.
var ErrSupportPath = errors.New("core: unknown support request")

var (
	reSupportGet  = regexp.MustCompile(`^/tickets(/[0-9a-f-]{36})?$`)
	reSupportPost = regexp.MustCompile(`^/tickets(/[0-9a-f-]{36}/(messages|human|close|attachments))?$`)
)

func (m *Manager) supportAPI(ctx context.Context) (SupportAPI, string, error) {
	s, ok := m.api.(SupportAPI)
	if !ok {
		return nil, "", ErrUnsupported
	}
	access, err := m.accessToken(ctx)
	if err != nil {
		return nil, "", err
	}
	return s, access, nil
}

// SupportGet relays a read of the user's tickets. Conversations pass
// through veilond and are never written to disk on the device.
func (m *Manager) SupportGet(ctx context.Context, path string) (json.RawMessage, error) {
	if !reSupportGet.MatchString(path) {
		return nil, ErrSupportPath
	}
	s, access, err := m.supportAPI(ctx)
	if err != nil {
		return nil, err
	}
	return s.SupportGet(ctx, access, path)
}

// SupportPost relays a ticket action (open, message, human, close,
// attachment).
func (m *Manager) SupportPost(ctx context.Context, path string, in any) (json.RawMessage, error) {
	if !reSupportPost.MatchString(path) {
		return nil, ErrSupportPath
	}
	s, access, err := m.supportAPI(ctx)
	if err != nil {
		return nil, err
	}
	return s.SupportPost(ctx, access, path, in)
}

// ClientVersion is the running client version.
func (m *Manager) ClientVersion() string { return m.opt.ClientVersion }
