package upload

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/credentials"
	"github.com/trace-dock/trace-dock/internal/api"
	"github.com/trace-dock/trace-dock/internal/core"
)

type uploadRoundTripFunc func(*http.Request) (*http.Response, error)

func (f uploadRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestSDKUploadCountsNewPayloadAndSkipsExistingObjects(t *testing.T) {
	for _, exists := range []bool{false, true} {
		name := "new upload"
		if exists {
			name = "reused object"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			data := bytes.Repeat([]byte("workspace contents\n"), 128)
			path := filepath.Join(dir, "staging", "workspace.tar.gz")
			if err := core.WritePrivate(path, data); err != nil {
				t.Fatal(err)
			}
			crc, size, err := core.CRC64File(path)
			if err != nil {
				t.Fatal(err)
			}
			puts := 0
			httpClient := &http.Client{Transport: uploadRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				headers := http.Header{"X-Oss-Request-Id": {"test-request"}}
				if r.Method == http.MethodHead && !exists {
					return &http.Response{StatusCode: http.StatusNotFound, Request: r, Header: headers, Body: io.NopCloser(strings.NewReader(`<Error><Code>NoSuchKey</Code></Error>`))}, nil
				}
				if r.Method == http.MethodPut {
					puts++
					actual, readErr := io.ReadAll(r.Body)
					r.Body.Close()
					if readErr != nil || !bytes.Equal(actual, data) {
						t.Errorf("transport received changed payload: %v", readErr)
					}
				}
				headers.Set("x-oss-hash-crc64ecma", crc)
				headers.Set("Content-Length", strconv.FormatInt(size, 10))
				headers.Set("ETag", `"test-etag"`)
				return &http.Response{StatusCode: http.StatusOK, Request: r, Header: headers, ContentLength: size, Body: io.NopCloser(strings.NewReader(""))}, nil
			})}
			u := &Uploader{Dir: dir, creds: api.Credentials{Bucket: "private-bucket"}}
			cfg := oss.LoadDefaultConfig().WithRegion("cn-hangzhou").WithEndpoint("https://oss.test").WithHttpClient(httpClient).
				WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test-ak", "test-sk", "test-sts"))
			u.client = oss.NewClient(cfg, u.trackNetworkBytes)
			o := &Object{Path: filepath.Join("staging", "workspace.tar.gz"), Artifact: core.Artifact{Key: "workspace.tar.gz", Size: size, CRC64: crc}}
			if err := u.put(context.Background(), o, nil); err != nil {
				t.Fatal(err)
			}
			wantBytes, wantPuts := size, 1
			if exists {
				wantBytes, wantPuts = 0, 0
			}
			if u.NetworkBytes() != wantBytes || puts != wantPuts {
				t.Fatalf("network bytes=%d PUTs=%d, want %d and %d", u.NetworkBytes(), puts, wantBytes, wantPuts)
			}
		})
	}
}

func TestUploadCounterCountsParallelAndReplayedBodies(t *testing.T) {
	var sent atomic.Int64
	client := &uploadHTTPClient{bytes: &sent, client: &http.Client{Transport: uploadRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			return nil, err
		}
		r.Body.Close()
		body, err := r.GetBody()
		if err != nil {
			return nil, err
		}
		defer body.Close()
		if _, err = io.Copy(io.Discard, body); err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: http.NoBody}, nil
	})}}
	const requests = 12
	data := strings.Repeat("a", 32<<10)
	var workers sync.WaitGroup
	for i := 0; i < requests; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			r, err := http.NewRequest(http.MethodPut, "https://oss.test/key", strings.NewReader(data))
			if err != nil {
				t.Error(err)
				return
			}
			response, err := client.Do(r)
			if err != nil {
				t.Error(err)
				return
			}
			response.Body.Close()
		}()
	}
	workers.Wait()
	if got, want := sent.Load(), int64(2*requests*len(data)); got != want {
		t.Fatalf("bytes sent=%d, want %d including replays", got, want)
	}
}
