// Package dotenv parses simple KEY=VALUE env files and applies them to the
// process environment. It exists so syscheckr can load secrets from a .env file
// without a shell `set -a; source` step. By default it does not override
// variables already present in the environment, so real env vars (systemd
// EnvironmentFile, CI secrets) win over the file.
package dotenv

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

// Parse reads KEY=VALUE lines from r and returns them in declaration order's
// final map. Rules:
//
//   - blank lines and lines beginning with '#' are ignored
//   - an optional leading "export " is stripped
//   - the key is everything before the first '='; it must be a valid env name
//   - single-quoted values are literal; double-quoted values interpret
//     \n \t \" \\ escapes; unquoted values are taken verbatim (trimmed)
//
// Inline comments after unquoted values are NOT stripped, so secrets and URLs
// containing '#' survive intact.
func Parse(r io.Reader) (map[string]string, error) {
	out := map[string]string{}
	sc := bufio.NewScanner(r)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")

		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			return nil, fmt.Errorf("line %d: missing '=' in %q", lineNo, line)
		}
		key := strings.TrimSpace(line[:eq])
		if !validKey(key) {
			return nil, fmt.Errorf("line %d: invalid key %q", lineNo, key)
		}
		val, err := unquote(strings.TrimSpace(line[eq+1:]))
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNo, err)
		}
		out[key] = val
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Load reads the env file at path, parses it, and sets each variable into the
// process environment. When override is false, variables already present are
// left untouched. It returns the number of variables actually set.
func Load(path string, override bool) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	vars, err := Parse(f)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", path, err)
	}
	set := 0
	for k, v := range vars {
		if !override {
			if _, exists := os.LookupEnv(k); exists {
				continue
			}
		}
		if err := os.Setenv(k, v); err != nil {
			return set, err
		}
		set++
	}
	return set, nil
}

func validKey(k string) bool {
	if k == "" {
		return false
	}
	for i, r := range k {
		switch {
		case r == '_':
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// unquote interprets quoting on a value. Single quotes are literal; double
// quotes process a small set of escapes; bare values are returned as-is.
func unquote(v string) (string, error) {
	if len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'' {
		return v[1 : len(v)-1], nil
	}
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		inner := v[1 : len(v)-1]
		var b strings.Builder
		for i := 0; i < len(inner); i++ {
			if inner[i] == '\\' && i+1 < len(inner) {
				switch inner[i+1] {
				case 'n':
					b.WriteByte('\n')
				case 't':
					b.WriteByte('\t')
				case '"':
					b.WriteByte('"')
				case '\\':
					b.WriteByte('\\')
				default:
					b.WriteByte('\\')
					b.WriteByte(inner[i+1])
				}
				i++
				continue
			}
			b.WriteByte(inner[i])
		}
		return b.String(), nil
	}
	return v, nil
}
