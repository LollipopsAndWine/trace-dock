package api

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestHTTPSAndLocalDevelopmentBoundary(t *testing.T) {
	for _, v := range []struct {
		url       string
		dev, want bool
	}{{"https://collector.test", false, true}, {"http://collector.test", true, true}, {"http://127.0.0.1:8000", false, false}, {"http://127.0.0.1:8000", true, true}, {"https://user:password@collector.test", false, false}, {"https://collector.test?token=secret", false, false}, {"file:///etc/passwd", true, false}} {
		if got := ValidURL(v.url, v.dev) == nil; got != v.want {
			t.Fatalf("URL boundary: %s", v.url)
		}
	}
}

func TestNoBodyEndpointsDoNotSendJSONBody(t *testing.T) {
	var paths []string
	client := New("https://collector.test", "token")
	client.HTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.URL.Path)
		if r.ContentLength != 0 {
			t.Errorf("%s content length = %d, want 0", r.URL.Path, r.ContentLength)
		}
		if got := r.Header.Get("Content-Type"); got != "" {
			t.Errorf("%s content type = %q, want empty", r.URL.Path, got)
		}
		body := `{"participant_id":"p","masked_email":"p***@example.com"}`
		if r.URL.Path != "/v1/identity/verify" {
			body = `{"endpoint":"https://oss.example","bucket":"bucket","prefix":"prefix/","region":"cn-hangzhou","access_key_id":"a","access_key_secret":"s","security_token":"t","expiration":"2026-01-01T00:00:00Z","path_style":false}`
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	if _, err := client.Verify(context.Background()); err != nil {
		t.Fatalf("Verify failed: %v", err)
	}
	if _, err := client.Credentials(context.Background(), "job"); err != nil {
		t.Fatalf("Credentials failed: %v", err)
	}
	if got, want := strings.Join(paths, ","), "/v1/identity/verify,/v1/collections/job/credentials"; got != want {
		t.Fatalf("request paths = %s, want %s", got, want)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
