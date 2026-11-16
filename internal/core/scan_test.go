package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestGitleaksRulesDetectSupportedSecretFormats(t *testing.T) {
	scanner, err := NewScanner()
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{
		"openai":         "sk-proj-" + strings.Repeat("Ab12", 8),
		"anthropic":      "sk-ant-" + strings.Repeat("Ab12", 8),
		"aws":            "AKIA" + strings.Repeat("A1", 8),
		"google":         "AIza" + strings.Repeat("A", 35),
		"github":         "ghp_" + strings.Repeat("Ab12", 8),
		"gitlab":         "glpat-" + strings.Repeat("Ab12", 8),
		"sendgrid":       "SG." + strings.Repeat("Ab12", 8) + "." + strings.Repeat("Cd34", 8),
		"slack":          "xoxb-" + strings.Repeat("A1", 8),
		"stripe":         "sk_test_" + strings.Repeat("Ab12", 8),
		"shopify":        "shpat_" + strings.Repeat("Ab12", 8),
		"huggingface":    "hf_" + strings.Repeat("Ab12", 8),
		"npm":            "npm_" + strings.Repeat("Ab12", 8),
		"linear":         "lin_api_" + strings.Repeat("Ab12", 8),
		"generic-key-32": "key-" + strings.Repeat("Ab12", 8),
		"jwt":            "eyJ" + strings.Repeat("A", 12) + "." + strings.Repeat("B", 12) + "." + strings.Repeat("C", 12),
		"pem":            "-----BEGIN OPENSSH PRIVATE KEY-----\nexample body\n-----END OPENSSH PRIVATE KEY-----",
	}
	for name, secret := range values {
		t.Run(name, func(t *testing.T) {
			payload, _ := json.Marshal(map[string]string{"content": secret})
			if !scanner.Hit(payload) {
				t.Fatal("supported credential shape was missed in a JSON string")
			}
			hit, err := scanner.Copy(io.Discard, bytes.NewReader(payload))
			if err != nil || !hit {
				t.Fatalf("stream detection: hit=%t err=%v", hit, err)
			}
		})
	}
}

func TestSecretScannerDoesNotApplyPIIOrGenericFieldRedaction(t *testing.T) {
	scanner, err := NewScanner()
	if err != nil {
		t.Fatal(err)
	}
	for _, payload := range []string{
		`api_key = "example_test_key"`,
		`password = "very_sensitive_value"`,
		`{"Authorization":"Bearer example_test_key","token":"participant-token-12345678"}`,
		`https://user:example_password@example.com`,
		`user@example.com 13800138000 192.168.1.1 张三`,
		`-----BEGIN OPENSSH PRIVATE KEY-----`,
		`sk-short ghp_short AIza_short`,
	} {
		if scanner.Hit([]byte(payload)) {
			t.Errorf("non-Gitleaks detection layer remained active for %q", payload)
		}
		var copied bytes.Buffer
		hit, err := scanner.Copy(&copied, strings.NewReader(payload))
		if err != nil || hit || copied.String() != payload {
			t.Errorf("clean content changed: hit=%t err=%v", hit, err)
		}
	}
}

func TestGitleaksStreamingHandlesLongSecretsAcrossReadBoundaries(t *testing.T) {
	scanner, err := NewScanner()
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{
		"-----BEGIN RSA PRIVATE KEY-----\n" + strings.Repeat("A", 160<<10) + "\n-----END RSA PRIVATE KEY-----",
		"eyJ" + strings.Repeat("A", 160<<10) + "." + strings.Repeat("B", 12) + "." + strings.Repeat("C", 12),
		"ghp_" + strings.Repeat("Ab12", 8),
	} {
		payload := strings.Repeat(" ", 65530) + secret
		hit, err := scanner.Copy(io.Discard, strings.NewReader(payload))
		if err != nil || !hit {
			t.Fatalf("secret spanning read boundaries was missed: hit=%t err=%v", hit, err)
		}
	}
	var copied bytes.Buffer
	clean := append([]byte(strings.Repeat("x", 200<<10)), 0, 0xff, 0xfe, '\n')
	hit, err := scanner.Copy(&copied, bytes.NewReader(clean))
	if err != nil || hit || !bytes.Equal(copied.Bytes(), clean) {
		t.Fatalf("clean binary file did not survive streaming unchanged: hit=%t err=%v", hit, err)
	}
}

type brokenScanReader struct{}

func (brokenScanReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

type shortScanWriter struct{}

func (shortScanWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }

func TestGitleaksScannerRejectsInvalidRulesAndPropagatesIOErrors(t *testing.T) {
	for _, data := range []string{
		`{`,
		`{"version":"2.1.0","rules":[]}`,
		`{"version":"1.0.0","rules":[{"id":"bad","regex":"x"}]}`,
		`{"version":"2.1.0","rules":[{"id":"bad","regex":"["}]}`,
	} {
		if _, err := loadSecretRules([]byte(data)); err == nil {
			t.Fatal("invalid secret rules accepted")
		}
	}
	scanner, err := NewScanner()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scanner.Copy(io.Discard, brokenScanReader{}); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("read error lost: %v", err)
	}
	if _, err := scanner.Copy(shortScanWriter{}, strings.NewReader("clean")); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("write error lost: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := scanner.CopyContext(ctx, io.Discard, strings.NewReader("clean")); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}
