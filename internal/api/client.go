package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/trace-dock/trace-dock/internal/core"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	Base, Token string
	HTTP        *http.Client
}
type Identity struct {
	ParticipantID string `json:"participant_id"`
	MaskedEmail   string `json:"masked_email"`
}
type Create struct {
	OperationID string `json:"client_operation_id"`
	DeviceID    string `json:"device_id"`
	Machine     string `json:"machine_name"`
	Tool        string `json:"tool"`
	Version     string `json:"cli_version"`
}
type Job struct {
	JobID        string     `json:"job_id"`
	Rules        core.Rules `json:"rules"`
	ObjectPrefix string     `json:"object_prefix"`
}
type Credentials struct {
	Endpoint        string    `json:"endpoint"`
	Bucket          string    `json:"bucket"`
	AccessKeyID     string    `json:"access_key_id"`
	AccessKeySecret string    `json:"access_key_secret"`
	SecurityToken   string    `json:"security_token"`
	Prefix          string    `json:"prefix"`
	Expiration      time.Time `json:"expiration"`
	PathStyle       bool      `json:"path_style"`
	Region          string    `json:"region"`
}
type Completion struct {
	ManifestKey  string `json:"manifest_key"`
	ManifestSHA  string `json:"manifest_sha256"`
	ManifestSize int64  `json:"manifest_size_bytes"`
}

func ValidURL(raw string, allowHTTP bool) error {
	u, e := url.Parse(raw)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("无效的服务地址")
	}
	if u.Scheme != "https" {
		if !(allowHTTP && u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1")) {
			return fmt.Errorf("服务地址必须使用 HTTPS")
		}
	}
	return nil
}
func New(base, token string) *Client {
	return &Client{Base: strings.TrimRight(base, "/"), Token: token, HTTP: &http.Client{Timeout: 45 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (c *Client) Call(ctx context.Context, method, path string, input, output any, key string) error {
	b, e := json.Marshal(input)
	if e != nil {
		return e
	}
	for attempt := 0; attempt < 4; attempt++ {
		req, e := http.NewRequestWithContext(ctx, method, c.Base+path, bytes.NewReader(b))
		if e != nil {
			return fmt.Errorf("invalid API request")
		}
		req.Header.Set("Authorization", "Bearer "+c.Token)
		req.Header.Set("Content-Type", "application/json")
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		requestClient := *c.HTTP
		if strings.HasSuffix(path, "/complete") {
			requestClient.Timeout = 30 * time.Minute
		}
		resp, e := requestClient.Do(req)
		retry := e != nil
		delay := time.Duration(1<<attempt) * time.Second
		if e == nil {
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				if output != nil {
					e = json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(output)
				}
				resp.Body.Close()
				if e != nil {
					return fmt.Errorf("invalid API response")
				}
				return nil
			}
			resp.Body.Close()
			retry = resp.StatusCode == 429 || resp.StatusCode >= 500
			if n, e := strconv.Atoi(resp.Header.Get("Retry-After")); e == nil && n > 0 {
				if n > 60 {
					n = 60
				}
				delay = time.Duration(n) * time.Second
			}
			if !retry {
				return fmt.Errorf("API 请求失败（HTTP %d）", resp.StatusCode)
			}
		}
		if attempt == 3 {
			return fmt.Errorf("网络或服务暂时不可用，任务可恢复")
		}
		if retry {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}
	}
	return fmt.Errorf("API unavailable")
}
func (c *Client) Verify(ctx context.Context) (Identity, error) {
	var v Identity
	e := c.Call(ctx, "POST", "/v1/identity/verify", struct{}{}, &v, "")
	return v, e
}
func (c *Client) Create(ctx context.Context, v Create) (Job, error) {
	var j Job
	e := c.Call(ctx, "POST", "/v1/collections", v, &j, v.OperationID)
	return j, e
}
func (c *Client) Patch(ctx context.Context, id string, v any) error {
	return c.Call(ctx, "PATCH", "/v1/collections/"+id, v, nil, "")
}
func (c *Client) Credentials(ctx context.Context, id string) (Credentials, error) {
	var v Credentials
	e := c.Call(ctx, "POST", "/v1/collections/"+id+"/credentials", struct{}{}, &v, "")
	return v, e
}
