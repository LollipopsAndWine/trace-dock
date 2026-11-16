package core

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
)

const SecretRulesVersion = "2.2.0"

// secretRulesJSON preserves guiji's second-layer rules. Literal credential
// fallback checks supplement them without changing the uploaded bytes.
//
//go:embed secret-rules.json
var secretRulesJSON []byte

var knownPublicSecretExamples = []*regexp.Regexp{
	// WebAuthn/FIDO SSH key algorithm identifiers begin with "sk-" but are
	// public protocol names, not OpenAI credentials.
	regexp.MustCompile(`(?i)\bsk-ecdsa-sha2-[A-Za-z0-9@._-]+\b`),
	regexp.MustCompile(`\b(?:AKIAIOSFODNN7EXAMPLE|AKIA111111111EXAMPLE|AKIA222222222EXAMPLE|AKIAI44QH8DHBEXAMPLE)\b`),
	regexp.MustCompile(`(?i)\b(?:API_KEY_GOES_HERE|example_test_key|example_password)\b`),
	regexp.MustCompile(`(?i)\b(?:password1|i_am_sensitive|iamsensitive|somekindofpassword123!@#|h\]6eszr}vj\*m)\b`),
}

type Scanner struct {
	pattern *regexp.Regexp
}

func NewScanner() (*Scanner, error) {
	return loadSecretRules(secretRulesJSON)
}

func loadSecretRules(data []byte) (*Scanner, error) {
	var config struct {
		Version string `json:"version"`
		Rules   []struct {
			ID    string `json:"id"`
			Regex string `json:"regex"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(data, &config); err != nil || config.Version != SecretRulesVersion || len(config.Rules) == 0 {
		return nil, fmt.Errorf("invalid embedded secret rules")
	}
	patterns := make([]string, 0, len(config.Rules))
	seen := map[string]bool{}
	for _, rule := range config.Rules {
		if rule.ID == "" || rule.Regex == "" || seen[rule.ID] {
			return nil, fmt.Errorf("invalid embedded secret rule")
		}
		seen[rule.ID] = true
		patterns = append(patterns, "(?:"+rule.Regex+")")
	}
	pattern, err := regexp.Compile(strings.Join(patterns, "|"))
	if err != nil {
		return nil, fmt.Errorf("invalid embedded secret pattern")
	}
	return &Scanner{pattern: pattern}, nil
}

func (s *Scanner) Hit(b []byte) bool {
	view := maskKnownPublicExamples(b)
	return s.pattern.Match(view) || (!hasBinaryMarker(view) && hasLiteralSecret(view))
}

func hasBinaryMarker(data []byte) bool {
	return bytes.IndexByte(data[:min(len(data), 8192)], 0) >= 0
}

func maskKnownPublicExamples(data []byte) []byte {
	var view []byte
	for _, pattern := range knownPublicSecretExamples {
		matches := pattern.FindAllIndex(data, -1)
		if len(matches) == 0 {
			continue
		}
		if view == nil {
			view = append([]byte(nil), data...)
		}
		for _, match := range matches {
			for i := match[0]; i < match[1]; i++ {
				view[i] = ' '
			}
		}
	}
	if view == nil {
		return data
	}
	return view
}

// Copy scans the exact bytes written to staging. It never emits matched values.
func (s *Scanner) Copy(dst io.Writer, src io.Reader) (bool, error) {
	return s.CopyContext(context.Background(), dst, src)
}

// CopyContext checks cancellation between bounded reads before writing more bytes.
func (s *Scanner) CopyContext(ctx context.Context, dst io.Writer, src io.Reader) (bool, error) {
	return s.CopyContextProgress(ctx, dst, src, nil)
}

func (s *Scanner) CopyContextProgress(ctx context.Context, dst io.Writer, src io.Reader, progress func(int64)) (bool, error) {
	reader := &scanReader{ctx: ctx, src: src, dst: dst, progress: progress, nextReport: 1 << 20}
	// MatchReader retains regexp state across reads, including arbitrarily long
	// PEM blocks and JWTs, without retaining the entire file in memory.
	hit := s.pattern.MatchReader(bufio.NewReaderSize(reader, 64*1024))
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if reader.err != nil {
		return false, reader.err
	}
	if progress != nil && reader.processed != reader.reported {
		progress(reader.processed)
	}
	return hit || reader.fallbackHit, nil
}

type scanReader struct {
	ctx                             context.Context
	src                             io.Reader
	dst                             io.Writer
	progress                        func(int64)
	processed, reported, nextReport int64
	err                             error
	tail                            []byte
	fallbackHit                     bool
	binary                          bool
}

func (r *scanReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		r.err = err
		return 0, err
	}
	n, err := r.src.Read(b)
	if ctxErr := r.ctx.Err(); ctxErr != nil {
		r.err = ctxErr
		return 0, ctxErr
	}
	if n > 0 {
		raw := append([]byte(nil), b[:n]...)
		if r.processed == 0 && hasBinaryMarker(raw) {
			r.binary = true
		}
		scanView := maskKnownPublicExamples(b[:n])
		window := append(r.tail, scanView...)
		if !r.binary && hasLiteralSecret(window) {
			r.fallbackHit = true
			return 0, io.EOF
		}
		// Fallback candidates are bounded to a few KiB. Preserve enough context
		// for names, calls and literals split across read boundaries.
		r.tail = append(r.tail[:0], window[max(0, len(window)-(32<<10)):]...)
		written, writeErr := r.dst.Write(raw)
		if writeErr == nil && written != n {
			writeErr = io.ErrShortWrite
		}
		if writeErr != nil {
			r.err = writeErr
			return 0, writeErr
		}
		r.processed += int64(n)
		copy(b[:n], scanView)
		if r.progress != nil && r.processed >= r.nextReport {
			r.progress(r.processed)
			r.reported = r.processed
			r.nextReport = r.processed + (1 << 20)
		}
	}
	if err != nil && err != io.EOF {
		r.err = err
	}
	return n, err
}
