package config

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// loadDotEnv fills in unset (or empty) environment variables from the dotenv
// file at path, so real environment variables always win. A missing file is
// not an error. Supported syntax: KEY=value, optional `export ` prefix,
// "double" or 'single' quoted values, `#` comment lines, and trailing
// ` # comments` on unquoted values.
func loadDotEnv(path string) error {
	f, err := os.Open(path) //nolint:gosec // path is a fixed, caller-chosen filename
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		key, val, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return fmt.Errorf("%s:%d: expected KEY=value", path, n)
		}
		if os.Getenv(key) != "" {
			continue
		}
		if val = parseValue(val); val != "" {
			if err := os.Setenv(key, val); err != nil {
				return fmt.Errorf("%s:%d: %w", path, n, err)
			}
		}
	}
	return sc.Err()
}

func parseValue(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') {
		if end := strings.IndexByte(v[1:], v[0]); end >= 0 {
			return v[1 : end+1]
		}
	}
	if i := strings.Index(v, " #"); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}
