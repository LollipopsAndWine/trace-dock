package core

import (
	"bytes"
	"io"
	"regexp"
)

var baselinePatterns = []string{
	`(?i)-----BEGIN (?:[A-Z0-9 ]+ )?PRIVATE KEY-----`,
	`\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`,
	`\bLTAI[A-Za-z0-9]{16,}\b`,
	`\b(?:sk-[A-Za-z0-9_-]{16,}|gh[pousr]_[A-Za-z0-9_]{20,}|github_pat_[A-Za-z0-9_]{20,}|xox[baprs]-[A-Za-z0-9-]{10,})`,
	`(?i)(?:["']?(?:password|passwd|api[_-]?key|access[_-]?key[_-]?secret|secret[_-]?key|client[_-]?secret|authorization|token)["']?\s*[:=]\s*["']?)[A-Za-z0-9_./+@:= -]{8,}`,
	`(?i)\bBearer\s+[A-Za-z0-9_.-]{12,}`,
	`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`,
	`(?i)https?://[^\s/:]+:[^\s/@]+@`,
	`(?i)(?:aws_secret_access_key|secret_access_key|private_key|api_key)\s*["']?\s*[:=]\s*["']?[A-Za-z0-9/+_=-]{8,}`,
	`\bAIza[A-Za-z0-9_-]{30,}\b`,
}

type Scanner struct {
	patterns []*regexp.Regexp
	token    []byte
}

func NewScanner(rules Rules, token string) (*Scanner, error) {
	s := &Scanner{token: []byte(token)}
	for _, p := range append(append([]string{}, baselinePatterns...), rules.SecretPatterns...) {
		r, e := regexp.Compile(p)
		if e != nil {
			return nil, e
		}
		s.patterns = append(s.patterns, r)
	}
	return s, nil
}
func (s *Scanner) Hit(b []byte) bool {
	if len(s.token) > 0 && bytes.Contains(b, s.token) {
		return true
	}
	for _, p := range s.patterns {
		if p.Match(b) {
			return true
		}
	}
	return false
}

// Copy scans the exact bytes written to staging. It never emits matched values.
func (s *Scanner) Copy(dst io.Writer, src io.Reader) (bool, error) {
	chunk := make([]byte, 64*1024)
	var tail []byte
	for {
		n, e := src.Read(chunk)
		if n > 0 {
			b := append(tail, chunk[:n]...)
			if s.Hit(b) {
				return true, nil
			}
			if _, err := dst.Write(chunk[:n]); err != nil {
				return false, err
			}
			start := len(b) - 32*1024
			if start < 0 {
				start = 0
			}
			tail = append([]byte(nil), b[start:]...)
		}
		if e == io.EOF {
			return false, nil
		}
		if e != nil {
			return false, e
		}
	}
}
