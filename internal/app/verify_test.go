package app

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/trace-dock/trace-dock/internal/api"
	"github.com/trace-dock/trace-dock/internal/ui"
)

type verificationTransport func(*http.Request) (*http.Response, error)

func (f verificationTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

type verificationOutput struct {
	strings.Builder
	frames    int
	refreshed chan struct{}
}

func (w *verificationOutput) Write(b []byte) (int, error) {
	if strings.Contains(string(b), "服务端校验中") {
		w.frames++
		if w.frames == 2 {
			close(w.refreshed)
		}
	}
	return w.Builder.Write(b)
}

func TestVerificationRefreshesUntilServerResponds(t *testing.T) {
	var output verificationOutput
	output.refreshed = make(chan struct{})
	u := &ui.UI{Out: &output, TTY: true}
	u.BeginUpload(ui.UploadSummary{TotalBytes: 1024})
	output.Reset()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	client := api.New("https://collector.test", "test-token")
	client.HTTP.Transport = verificationTransport(func(r *http.Request) (*http.Response, error) {
		select {
		case <-output.refreshed:
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"state":"completed","verification":"crc64_verified"}`))}, nil
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	})
	receipt, err := completeWithProgress(ctx, u, client, "job", api.Completion{})
	if err != nil || receipt["state"] != "completed" || output.frames < 2 {
		t.Fatalf("verification did not keep refreshing during the request: frames=%d receipt=%v err=%v", output.frames, receipt, err)
	}
	if strings.Contains(output.String(), "100%") || !strings.Contains(output.String(), "请保持终端开启") {
		t.Fatalf("pending verification appeared complete: %s", output.String())
	}
}

func TestVerificationErrorDoesNotShowSuccess(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"API error", 403, `{}`},
		{"not completed", 200, `{"state":"verifying"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output strings.Builder
			u := &ui.UI{Out: &output}
			u.BeginUpload(ui.UploadSummary{TotalBytes: 1024})
			output.Reset()
			client := api.New("https://collector.test", "test-token")
			client.HTTP.Transport = verificationTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})
			if receipt, err := completeWithProgress(context.Background(), u, client, "job", api.Completion{}); err == nil || receipt != nil {
				t.Fatalf("unverified response accepted: receipt=%v err=%v", receipt, err)
			}
			if !strings.Contains(output.String(), "已中断") || strings.Contains(output.String(), "100%") || strings.Contains(output.String(), "07/07") {
				t.Fatalf("verification error displayed completion: %s", output.String())
			}
		})
	}
}
