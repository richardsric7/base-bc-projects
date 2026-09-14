package services

import (
	"encoding/json"
	"fmt"
	"net/url"
)

// deepLinkResult holds a minted shortlink's public URL and QR image URL,
// ready to embed in a servicelinks response alongside an approval or a
// payment-request link.
type deepLinkResult struct {
	ShortURL string
	QRURL    string
}

// mintDeepLink builds a shortlink target URL encoding action - the only
// field the original mobile app's deep-link dispatcher actually reads to
// decide which screen to show (PLAN.md §14.2 item 2) - plus params, mints
// it via the shortlink component, and returns its public short URL and QR
// image URL (ShortURL + "/qr", per shortlink/controllers.go's route
// shape). If Shortlink was never wired up (e.g. a unit test that doesn't
// need QR minting), this is a no-op returning a zero-value result rather
// than an error, so callers that don't care about QR codes aren't forced
// to construct a shortlink service.
func (s *Service) mintDeepLink(action string, params map[string]string, metadata map[string]string) (deepLinkResult, error) {
	if s.Shortlink == nil {
		return deepLinkResult{}, nil
	}

	q := url.Values{}
	q.Set("action", action)
	for k, v := range params {
		if v != "" {
			q.Set(k, v)
		}
	}
	targetURL := fmt.Sprintf("%s/deeplink?%s", s.Shortlink.BaseURL, q.Encode())

	var meta string
	if len(metadata) > 0 {
		if encoded, err := json.Marshal(metadata); err == nil {
			meta = string(encoded)
		}
	}

	link, err := s.Shortlink.CreateLink(targetURL, meta)
	if err != nil {
		return deepLinkResult{}, err
	}
	shortURL := s.Shortlink.ShortURL(link.ShortCode)
	return deepLinkResult{ShortURL: shortURL, QRURL: shortURL + "/qr"}, nil
}
