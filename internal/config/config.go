// Package config defines the YAML schema for syscheckr and loads it, expanding
// ${ENV} references and validating structure before checks/reporters are built.
package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the top-level configuration document.
type Config struct {
	Defaults  Defaults         `yaml:"defaults"`
	Checks    []CheckConfig    `yaml:"checks"`
	Reporters []ReporterConfig `yaml:"reporters"`
	State     StateConfig      `yaml:"state"`
}

// Defaults holds settings applied to checks that do not override them.
type Defaults struct {
	Timeout time.Duration `yaml:"timeout"`
}

// StateConfig configures the on-disk state store used for reporter dedupe.
type StateConfig struct {
	Path string `yaml:"path"`
}

// CheckConfig is one entry in the checks list.
type CheckConfig struct {
	Name     string         `yaml:"name"`
	Type     string         `yaml:"type"`
	Schedule string         `yaml:"schedule"` // cron expression, daemon mode only
	Tags     []string       `yaml:"tags"`
	Timeout  time.Duration  `yaml:"timeout"` // overrides Defaults.Timeout
	Config   map[string]any `yaml:"config"`
}

// ReporterConfig is one entry in the reporters list.
type ReporterConfig struct {
	Name        string         `yaml:"name"`
	Type        string         `yaml:"type"`
	MinSeverity string         `yaml:"min_severity"`
	Checks      []string       `yaml:"checks"`
	Tags        []string       `yaml:"tags"`
	OnlyFailing bool           `yaml:"only_failing"`
	Config      map[string]any `yaml:"config"`
}

// envRef matches ${VAR} and ${VAR:-default} style references.
var envRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-([^}]*))?\}`)

// expandEnv replaces ${VAR} / ${VAR:-default} occurrences in the raw document
// with environment values. Missing variables without a default expand to "".
func expandEnv(raw []byte) []byte {
	return envRef.ReplaceAllFunc(raw, func(m []byte) []byte {
		sub := envRef.FindSubmatch(m)
		name := string(sub[1])
		if v, ok := os.LookupEnv(name); ok && v != "" {
			return []byte(v)
		}
		return sub[2] // default (may be empty)
	})
}

// Load reads, env-expands, parses, and validates the config at path.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	return Parse(raw)
}

// Parse env-expands, decodes, applies defaults, and validates a raw document.
func Parse(raw []byte) (*Config, error) {
	expanded := expandEnv(raw)

	var cfg Config
	dec := yaml.NewDecoder(strings.NewReader(string(expanded)))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) applyDefaults() {
	if c.Defaults.Timeout == 0 {
		c.Defaults.Timeout = 10 * time.Second
	}
	for i := range c.Checks {
		if c.Checks[i].Timeout == 0 {
			c.Checks[i].Timeout = c.Defaults.Timeout
		}
	}
}

// Validate checks structural invariants: non-empty unique names, present types,
// and parseable min_severity values. It does not construct checks/reporters.
func (c *Config) Validate() error {
	var errs []string

	if len(c.Checks) == 0 {
		errs = append(errs, "at least one check is required")
	}
	seenCheck := map[string]bool{}
	for i, ch := range c.Checks {
		where := fmt.Sprintf("checks[%d]", i)
		if ch.Name == "" {
			errs = append(errs, where+": name is required")
		} else if seenCheck[ch.Name] {
			errs = append(errs, fmt.Sprintf("%s: duplicate check name %q", where, ch.Name))
		}
		seenCheck[ch.Name] = true
		if ch.Type == "" {
			errs = append(errs, fmt.Sprintf("%s (%s): type is required", where, ch.Name))
		}
	}

	seenRep := map[string]bool{}
	for i, rp := range c.Reporters {
		where := fmt.Sprintf("reporters[%d]", i)
		if rp.Name == "" {
			errs = append(errs, where+": name is required")
		} else if seenRep[rp.Name] {
			errs = append(errs, fmt.Sprintf("%s: duplicate reporter name %q", where, rp.Name))
		}
		seenRep[rp.Name] = true
		if rp.Type == "" {
			errs = append(errs, fmt.Sprintf("%s (%s): type is required", where, rp.Name))
		}
		if rp.MinSeverity != "" {
			if !validSeverity(rp.MinSeverity) {
				errs = append(errs, fmt.Sprintf("%s (%s): invalid min_severity %q", where, rp.Name, rp.MinSeverity))
			}
		}
		// Referenced check names should exist, to catch typos early.
		for _, cn := range rp.Checks {
			if !seenCheck[cn] {
				errs = append(errs, fmt.Sprintf("%s (%s): references unknown check %q", where, rp.Name, cn))
			}
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("invalid config:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}

func validSeverity(s string) bool {
	switch strings.ToLower(s) {
	case "ok", "warn", "warning", "crit", "critical", "unknown":
		return true
	default:
		return false
	}
}
