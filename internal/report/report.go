// Package report defines the Reporter interface, a registry of reporter types,
// and severity/check/tag routing that decides which results each reporter sees.
package report

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/Krakaw/syscheckr/internal/check"
)

// Reporter delivers a set of check results to some destination (log, Slack,
// webhook, Linear, ...). Report should be safe to call concurrently across
// reporters but receives only the results that passed this reporter's routing.
type Reporter interface {
	Name() string
	Report(ctx context.Context, results []check.Result) error
}

// Factory constructs a Reporter from its name and raw config map.
type Factory func(name string, cfg map[string]any) (Reporter, error)

var (
	registryMu sync.RWMutex
	registry   = map[string]Factory{}
)

// Register adds a reporter factory under typeName, panicking on duplicates.
func Register(typeName string, f Factory) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, dup := registry[typeName]; dup {
		panic(fmt.Sprintf("report: type %q already registered", typeName))
	}
	registry[typeName] = f
}

// New constructs a reporter of the given type.
func New(typeName, name string, cfg map[string]any) (Reporter, error) {
	registryMu.RLock()
	f, ok := registry[typeName]
	registryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown reporter type %q", typeName)
	}
	return f(name, cfg)
}

// Types returns the sorted list of registered reporter type names.
func Types() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Route describes which results a reporter should receive. A zero Route (no
// filters) matches every result.
type Route struct {
	MinSeverity check.Status // results below this severity are dropped
	Checks      []string     // if non-empty, only these check names match
	Tags        []string     // if non-empty, result must carry one of these tags
	OnlyFailing bool         // if true, OK results are dropped regardless of MinSeverity
}

// Filter returns the subset of results that match the route.
func (r Route) Filter(results []check.Result) []check.Result {
	checkSet := toSet(r.Checks)
	tagSet := toSet(r.Tags)
	out := make([]check.Result, 0, len(results))
	for _, res := range results {
		if r.OnlyFailing && !res.Status.IsFailing() {
			continue
		}
		if res.Status.Severity() < r.MinSeverity.Severity() {
			continue
		}
		if len(checkSet) > 0 && !checkSet[res.Check] {
			continue
		}
		if len(tagSet) > 0 && !hasAnyTag(res.Tags, tagSet) {
			continue
		}
		out = append(out, res)
	}
	return out
}

// verboseDetailKeys are detail keys that can carry raw, potentially sensitive
// content (log lines, command stdout). Reporters that ship details to external
// destinations can strip them via redactedResults.
var verboseDetailKeys = map[string]bool{"samples": true, "output": true}

// redactedResults returns copies of results with verbose/secret-bearing detail
// keys removed, so log samples and command output are not sent off-box. The
// inputs are not mutated.
func redactedResults(results []check.Result) []check.Result {
	out := make([]check.Result, len(results))
	for i, r := range results {
		out[i] = r
		if len(r.Details) == 0 {
			continue
		}
		cleaned := make(map[string]any, len(r.Details))
		for k, v := range r.Details {
			if verboseDetailKeys[k] {
				continue
			}
			cleaned[k] = v
		}
		out[i].Details = cleaned
	}
	return out
}

func toSet(items []string) map[string]bool {
	if len(items) == 0 {
		return nil
	}
	s := make(map[string]bool, len(items))
	for _, i := range items {
		s[i] = true
	}
	return s
}

func hasAnyTag(tags []string, set map[string]bool) bool {
	for _, t := range tags {
		if set[t] {
			return true
		}
	}
	return false
}
