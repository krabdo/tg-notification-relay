package main

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestBarkRepairIsScoped(t *testing.T) {
	for _, tc := range []struct {
		url, body string
		repair    bool
	}{
		{"https://bark.example/push", `{"device_key":"key","body":"hello"}`, true},
		{"https://other.example/push", `{"device_key":"key","body":"hello"}`, false},
		{"https://bark.example/push", `{"device_key":"other","body":"hello"}`, false},
	} {
		transport := newBarkTransport(roundTripFunc(func(r *http.Request) (*http.Response, error) {
			b, _ := io.ReadAll(r.Body)
			if strings.Contains(string(b), `"url":"tg://"`) != tc.repair {
				t.Fatalf("unexpected body %s", b)
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(""))}, nil
		}))
		transport.destinations[barkDestination{"https://bark.example/push", "key"}] = true
		req, _ := http.NewRequest(http.MethodPost, tc.url, strings.NewReader(tc.body))
		resp, err := transport.RoundTrip(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
}
func TestBarkDefaultsToHTTPS(t *testing.T) {
	_, err := newNotifier([]string{"bark://:key@bark.example/"}, &pushState{enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	transport, ok := http.DefaultClient.Transport.(*barkTransport)
	if !ok || !transport.destinations[barkDestination{"https://bark.example/push", "key"}] {
		t.Fatal("missing HTTPS default")
	}
}
