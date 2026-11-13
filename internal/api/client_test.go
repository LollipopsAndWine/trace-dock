package api

import "testing"

func TestHTTPSAndLocalDevelopmentBoundary(t *testing.T) {
	for _, v := range []struct {
		url       string
		dev, want bool
	}{{"https://collector.test", false, true}, {"http://collector.test", true, false}, {"http://127.0.0.1:8000", false, false}, {"http://127.0.0.1:8000", true, true}, {"https://user:password@collector.test", false, false}, {"https://collector.test?token=secret", false, false}, {"file:///etc/passwd", true, false}} {
		if got := ValidURL(v.url, v.dev) == nil; got != v.want {
			t.Fatalf("URL boundary: %s", v.url)
		}
	}
}
