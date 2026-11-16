package core

import (
	"bytes"
	"regexp"
	"strconv"
	"strings"
)

var (
	secretField       = regexp.MustCompile(`(?i)\b(?:[a-z0-9]+[_-])*(?:password|passwd|pwd|api[_-]?key|api[_-]?token|client[_-]?secret|secret[_-]?key|access[_-]?key[_-]?secret)\b`)
	envCall           = regexp.MustCompile(`\b(?:os\.)?(?:getenv|environ\.get)\s*\(`)
	secretHint        = regexp.MustCompile(`(?i)(?:password|passwd|pwd|api[_-]?key|api[_-]?token|client[_-]?secret|secret[_-]?key|access[_-]?key[_-]?secret|(?:os\.)?getenv\s*\(|environ\.get\s*\()`)
	variableReference = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*$`)
	envReference      = regexp.MustCompile(`^[A-Z][A-Z0-9_]*(?:_(?:PASSWORD|PASSWD|PWD|API_KEY|API_TOKEN|CLIENT_SECRET|SECRET|SECRET_ACCESS_KEY|ACCESS_KEY|ACCESS_KEY_ID|ACCESS_KEY_SECRET|TOKEN))$`)
	arithmeticValue   = regexp.MustCompile(`^[A-Z][A-Z0-9_]*(?:/[A-Z0-9_]+)+$`)
	objectIdentifier  = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)+$`)
	credentialRef     = regexp.MustCompile(`(?i)^(?:aws[_-]?(?:secret[_-]?access[_-]?key|access[_-]?key(?:[_-]?id)?|secret[_-]?key)|secret[_-]?access[_-]?key|challenge[_-]?password|username|accesskeyid|secretaccesskey)$`)
	codeKeyword       = regexp.MustCompile(`(?i)^(?:try|if|else|elif|except|finally|return|raise|pass|continue|break|class|def|for|while|with):?$`)
)

// hasLiteralSecret detects values in credential assignments and environment
// defaults. It inspects a view of the original bytes and never replaces them.
func hasLiteralSecret(data []byte) bool {
	if !secretHint.Match(data) {
		return false
	}
	for _, field := range secretField.FindAllIndex(data, -1) {
		if field[1]-field[0] > 128 {
			continue
		}
		pos := field[1]
		// JSON keys may have multiple layers of escaped closing quotes.
		if _, _, end, ok := quoteAt(data, pos); ok {
			pos = end
		}
		pos = secretSpace(data, pos)
		if pos < len(data) && data[pos] == ']' {
			pos = secretSpace(data, pos+1)
		}
		if pos >= len(data) || (data[pos] != '=' && data[pos] != ':') {
			continue
		}
		pos = secretSpace(data, pos+1)
		if pos >= len(data) || data[pos] == '=' {
			continue
		}
		if call := envCall.FindIndex(data[pos:]); call != nil && call[0] == 0 {
			if hasSecretEnvDefault(data, pos+call[1], true) {
				return true
			}
			continue
		}
		if value, end, ok := secretLiteral(data, pos); ok && plausibleSecret(value) {
			next := secretSpace(data, end)
			if next == len(data) || !bytes.ContainsRune([]byte("+%.*"), rune(data[next])) {
				return true
			}
		}
	}
	// Also cover getenv("DB_PASSWORD", "...") used inside another expression.
	for _, call := range envCall.FindAllIndex(data, -1) {
		if hasSecretEnvDefault(data, call[1], false) {
			return true
		}
	}
	return false
}

func hasSecretEnvDefault(data []byte, pos int, sensitiveAssignment bool) bool {
	name, pos, ok := quotedSecret(data, secretSpace(data, pos))
	if !ok {
		return false
	}
	if !sensitiveAssignment {
		field := secretField.FindStringIndex(name)
		if field == nil || field[0] != 0 || field[1] != len(name) {
			return false
		}
	}
	pos = secretSpace(data, pos)
	if pos >= len(data) || data[pos] != ',' {
		return false
	}
	pos = secretSpace(data, pos+1)
	if bytes.HasPrefix(data[pos:], []byte("default")) {
		pos = secretSpace(data, pos+len("default"))
		if pos >= len(data) || data[pos] != '=' {
			return false
		}
		pos = secretSpace(data, pos+1)
	}
	value, end, ok := quotedSecret(data, pos)
	if !ok || !plausibleSecret(value) {
		return false
	}
	end = secretSpace(data, end)
	// Expressions such as os.getenv("KEY", "prefix" + VARIABLE) are not
	// complete literal defaults; do not mistake a fragment for a credential.
	return end < len(data) && (data[end] == ')' || data[end] == ',')
}

func secretSpace(data []byte, pos int) int {
	limit := min(len(data), pos+256)
	for pos < limit {
		if data[pos] == ' ' || data[pos] == '\t' || data[pos] == '\n' || data[pos] == '\r' {
			pos++
			continue
		}
		start := pos
		for pos < limit && data[pos] == '\\' {
			pos++
		}
		if pos > start && pos < limit && (data[pos] == 'n' || data[pos] == 'r' || data[pos] == 't') {
			pos++
			continue
		}
		return start
	}
	return pos
}

func quoteAt(data []byte, pos int) (quote byte, slashes, end int, ok bool) {
	start := pos
	for pos < len(data) && data[pos] == '\\' && pos-start <= 63 {
		pos++
	}
	if pos < len(data) && (data[pos] == '"' || data[pos] == '\'') {
		return data[pos], pos - start, pos + 1, true
	}
	return 0, 0, start, false
}

func quotedSecret(data []byte, pos int) (string, int, bool) {
	quote, slashes, start, ok := quoteAt(data, pos)
	if !ok {
		return "", pos, false
	}
	limit := min(len(data), start+4096)
	for i := start; i < limit; {
		if data[i] == '\n' || data[i] == '\r' {
			return "", pos, false
		}
		if q, count, end, found := quoteAt(data, i); found {
			if q == quote && count == slashes {
				return string(data[start:i]), end, true
			}
			i = end
		} else {
			i++
		}
	}
	return "", pos, false
}

func secretLiteral(data []byte, pos int) (string, int, bool) {
	if _, _, _, quoted := quoteAt(data, pos); quoted {
		return quotedSecret(data, pos)
	}
	// .env/INI values may be unquoted. Skip Python identifiers, attribute
	// references and function calls rather than guessing their runtime values.
	end := pos
	for end < len(data) && end-pos <= 1024 && !bytes.ContainsRune([]byte(" \t\r\n,;)}]\"'"), rune(data[end])) {
		if data[end] == '\\' {
			if _, _, _, quoted := quoteAt(data, end); quoted || secretSpace(data, end) > end {
				break
			}
		}
		end++
	}
	if end-pos > 1024 {
		return "", pos, false
	}
	value := string(data[pos:end])
	if variableReference.MatchString(value) || strings.ContainsAny(value, "(\\") {
		return "", pos, false
	}
	// A field lookup, type annotation, mapping expression, or other source
	// expression is not a literal credential. This avoids flagging dependency
	// implementation code such as secret_key=credentials["SecretAccessKey"].
	if strings.ContainsAny(value, "[]{}") {
		return "", pos, false
	}
	return value, end, true
}

func plausibleSecret(value string) bool {
	// Interpret JSON string escapes for placeholder checks only; this temporary
	// value is never written back to the source or the upload staging file.
	for layer := 0; layer < 6 && strings.Contains(value, "\\"); layer++ {
		decoded, err := strconv.Unquote("\"" + value + "\"")
		if err != nil || decoded == value {
			break
		}
		value = decoded
	}
	value = strings.TrimSpace(value)
	if len(value) < 4 || len(value) > 1024 || strings.ContainsAny(value, " \t\r\n") {
		return false
	}
	if codeKeyword.MatchString(value) {
		return false
	}
	if strings.HasPrefix(value, "$") || strings.HasPrefix(value, "{{") || strings.HasPrefix(value, "<") || strings.HasPrefix(value, "%") || envReference.MatchString(value) {
		return false
	}
	if objectIdentifier.MatchString(value) {
		return false
	}
	switch strings.ToLower(value) {
	case "username", "password", "secret", "secretkey", "secretaccesskey", "accesskeyid", "awssecretkey", "awsaccesskeyid", "awssecretaccesskey", "clientsecret", "apikey", "modelapikey", "challengepassword":
		return false
	}
	if credentialRef.MatchString(value) {
		return false
	}
	if strings.Trim(value, "*xX._-") == "" {
		return false
	}
	lower := strings.ToLower(value)
	switch lower {
	case "none", "null", "undefined", "redacted", "sensitive", "placeholder", "example", "dummy", "pasword", "password1", "iamsensitive", "somekindofpassword123!@#", "h]6eszr}vj*m", "your_password", "your_api_key", "test_password", "test_api_key", "very_sensitive_value":
		return false
	}
	if arithmeticValue.MatchString(value) {
		return false
	}
	for _, prefix := range []string{"example_", "example-", "dummy_", "dummy-", "placeholder_", "placeholder-", "your_", "your-"} {
		if strings.HasPrefix(lower, prefix) {
			return false
		}
	}
	if strings.HasSuffix(lower, "_goes_here") || strings.HasSuffix(lower, "_example") || strings.HasSuffix(lower, "-example") {
		return false
	}
	return true
}
