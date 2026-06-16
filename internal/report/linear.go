package report

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/keith/syscheckr/internal/check"
	"github.com/keith/syscheckr/internal/confutil"
	"github.com/keith/syscheckr/internal/state"
)

const linearAPI = "https://api.linear.app/graphql"

// linearReporter creates a Linear issue per failing check via the GraphQL API.
// To avoid filing a duplicate ticket every run, it records the time each check
// last filed an issue in a JSON state store and suppresses re-filing within
// dedupe_window.
type linearReporter struct {
	name         string
	apiKey       string
	teamID       string
	labelIDs     []string
	dedupeWindow time.Duration
	redact       bool
	store        *state.Store
	client       *http.Client
	now          func() time.Time
	overrideURL  string // api_url config key, for testing against a fake server
}

func init() {
	Register("linear", newLinearReporter)
}

// newLinearReporter config keys:
//
//	api_key:       Linear API key (required)
//	team_id:       team UUID to create issues under (required)
//	label_ids:     list of label UUIDs to attach (optional)
//	dedupe_window: suppress re-filing for the same check within this window
//	               (default 24h; 0 disables dedupe)
//	redact:        strip log samples / command output from the issue body
//	               (default false)
//	state_path:    JSON dedupe store path (default "syscheckr-state.json")
func newLinearReporter(name string, cfg map[string]any) (Reporter, error) {
	m := confutil.New(name, cfg)
	r := &linearReporter{
		name:         name,
		apiKey:       m.Required("api_key"),
		teamID:       m.Required("team_id"),
		dedupeWindow: m.Duration("dedupe_window", 24*time.Hour),
		redact:       m.Bool("redact", false),
		client:       &http.Client{Timeout: m.Duration("timeout", 15*time.Second)},
		now:          time.Now,
		overrideURL:  m.StringDefault("api_url", ""),
	}
	if raw, ok := cfg["label_ids"].([]any); ok {
		for _, l := range raw {
			r.labelIDs = append(r.labelIDs, fmt.Sprint(l))
		}
	}
	statePath := m.StringDefault("state_path", "syscheckr-state.json")
	if err := m.Err(); err != nil {
		return nil, err
	}
	store, err := state.Open(statePath)
	if err != nil {
		return nil, fmt.Errorf("%s: open dedupe state: %w", name, err)
	}
	r.store = store
	return r, nil
}

func (r *linearReporter) Name() string { return r.name }

func (r *linearReporter) Report(ctx context.Context, results []check.Result) error {
	if r.redact {
		results = redactedResults(results)
	}
	var errs []error
	for _, res := range results {
		key := "linear:" + res.Check
		now := r.now()
		if r.store.Seen(key, r.dedupeWindow, now) {
			continue // an issue was filed for this check recently
		}
		if err := r.createIssue(ctx, res); err != nil {
			errs = append(errs, fmt.Errorf("check %q: %w", res.Check, err))
			continue
		}
		if err := r.store.Mark(key, now); err != nil {
			errs = append(errs, fmt.Errorf("check %q: record dedupe: %w", res.Check, err))
		}
	}
	if len(errs) > 0 {
		return joinErrs(errs)
	}
	return nil
}

// graphqlRequest is the JSON body for a Linear GraphQL call.
type graphqlRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

// graphqlResponse captures errors and the issueCreate success flag.
type graphqlResponse struct {
	Data struct {
		IssueCreate struct {
			Success bool `json:"success"`
			Issue   struct {
				ID         string `json:"id"`
				Identifier string `json:"identifier"`
			} `json:"issue"`
		} `json:"issueCreate"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

const issueCreateMutation = `mutation IssueCreate($input: IssueCreateInput!) {
  issueCreate(input: $input) { success issue { id identifier } }
}`

func (r *linearReporter) createIssue(ctx context.Context, res check.Result) error {
	input := map[string]any{
		"teamId":      r.teamID,
		"title":       fmt.Sprintf("[%s] %s: %s", res.Status, res.Check, res.Summary),
		"description": issueBody(res),
	}
	if len(r.labelIDs) > 0 {
		input["labelIds"] = r.labelIDs
	}
	body, err := json.Marshal(graphqlRequest{
		Query:     issueCreateMutation,
		Variables: map[string]any{"input": input},
	})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", r.apiKey)

	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("linear request: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	io.Copy(io.Discard, resp.Body) // drain remainder so the conn is reused
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("linear status %d: %s", resp.StatusCode, string(raw))
	}
	var gr graphqlResponse
	if err := json.Unmarshal(raw, &gr); err != nil {
		return fmt.Errorf("decode linear response: %w", err)
	}
	if len(gr.Errors) > 0 {
		return fmt.Errorf("linear error: %s", gr.Errors[0].Message)
	}
	if !gr.Data.IssueCreate.Success {
		return fmt.Errorf("linear issueCreate reported failure")
	}
	return nil
}

// endpoint allows tests to override the Linear API URL via the LINEAR_API_URL
// env var; production always hits the real API.
func (r *linearReporter) endpoint() string {
	if r.overrideURL != "" {
		return r.overrideURL
	}
	return linearAPI
}

func issueBody(res check.Result) string {
	b := fmt.Sprintf("**Status:** %s\n\n**Summary:** %s\n", res.Status, res.Summary)
	if res.Error != "" {
		b += fmt.Sprintf("\n**Error:** %s\n", res.Error)
	}
	if len(res.Details) > 0 {
		b += "\n**Details:**\n"
		for k, v := range res.Details {
			b += fmt.Sprintf("- %s: %v\n", k, v)
		}
	}
	b += fmt.Sprintf("\n_Filed by syscheckr at %s_\n", res.Timestamp.Format(time.RFC3339))
	return b
}

func joinErrs(errs []error) error {
	if len(errs) == 1 {
		return errs[0]
	}
	msg := "multiple errors:"
	for _, e := range errs {
		msg += "\n  - " + e.Error()
	}
	return fmt.Errorf("%s", msg)
}
