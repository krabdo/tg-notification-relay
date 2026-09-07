package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
)

type barkDestination struct{ endpoint, key string }
type barkTransport struct {
	base         http.RoundTripper
	destinations map[barkDestination]bool
}

func newBarkTransport(base http.RoundTripper) *barkTransport {
	return &barkTransport{base: base, destinations: make(map[barkDestination]bool)}
}

// Shoutrrr v0.8.0 declares the Bark URL property but omits it when creating
// PushPayload. Keep the pinned upstream service and repair only requests to
// explicitly configured Bark endpoint/device pairs. Remove after upgrading to
// an upstream release containing the fix. No credentials are added to logs.
func (t *barkTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	matching := false
	for d := range t.destinations {
		if d.endpoint == req.URL.String() {
			matching = true
			break
		}
	}
	if !matching || req.Method != http.MethodPost || req.Body == nil {
		return t.base.RoundTrip(req)
	}
	body, err := io.ReadAll(req.Body)
	req.Body.Close()
	if err != nil {
		return nil, err
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal(body, &payload) == nil {
		var key string
		_ = json.Unmarshal(payload["device_key"], &key)
		if t.destinations[barkDestination{req.URL.String(), key}] {
			payload["url"] = json.RawMessage(`"tg://"`)
			body, err = json.Marshal(payload)
			if err != nil {
				return nil, err
			}
		}
	}
	clone := req.Clone(req.Context())
	clone.Body = io.NopCloser(bytes.NewReader(body))
	clone.ContentLength = int64(len(body))
	clone.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
	return t.base.RoundTrip(clone)
}
