package upload

import (
	"io"
	"net/http"
	"sync/atomic"

	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
)

// NetworkBytes counts upload payload read by the HTTP transport in this run,
// including retransmissions. Restored parts and objects found by HEAD only
// affect logical progress and are never counted here.
func (u *Uploader) NetworkBytes() int64 { return u.networkBytes.Load() }

func (u *Uploader) trackNetworkBytes(options *oss.Options) {
	options.HttpClient = &uploadHTTPClient{client: options.HttpClient, bytes: &u.networkBytes}
}

type uploadHTTPClient struct {
	client oss.HTTPClient
	bytes  *atomic.Int64
}

func (c *uploadHTTPClient) Do(request *http.Request) (*http.Response, error) {
	if request.Method != http.MethodPut || request.Body == nil || request.Body == http.NoBody {
		return c.client.Do(request)
	}
	// Keep the SDK's signing, CRC64 checking, retries, connection pool and
	// timeouts. Only observe the request body consumed by its HTTP client.
	tracked := request.Clone(request.Context())
	tracked.Body = &uploadBody{ReadCloser: request.Body, bytes: c.bytes}
	if request.GetBody != nil {
		tracked.GetBody = func() (io.ReadCloser, error) {
			body, err := request.GetBody()
			if err != nil {
				return nil, err
			}
			return &uploadBody{ReadCloser: body, bytes: c.bytes}, nil
		}
	}
	return c.client.Do(tracked)
}

type uploadBody struct {
	io.ReadCloser
	bytes *atomic.Int64
}

func (b *uploadBody) Read(data []byte) (int, error) {
	n, err := b.ReadCloser.Read(data)
	if n > 0 {
		b.bytes.Add(int64(n))
	}
	return n, err
}
