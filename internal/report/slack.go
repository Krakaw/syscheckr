package report

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/keith/syscheckr/internal/check"
	"github.com/keith/syscheckr/internal/confutil"
)

// slackReporter posts a message to a Slack incoming webhook, one attachment per
// result colored by severity.
type slackReporter struct {
	name       string
	webhookURL string
	username   string
	channel    string
	client     *http.Client
}

func init() {
	Register("slack", newSlackReporter)
}

// newSlackReporter config keys:
//
//	webhook_url: Slack incoming webhook URL (required)
//	username:    override the bot username (optional)
//	channel:     override the destination channel (optional)
//	timeout:     request timeout (default 15s)
func newSlackReporter(name string, cfg map[string]any) (Reporter, error) {
	m := confutil.New(name, cfg)
	r := &slackReporter{
		name:       name,
		webhookURL: m.Required("webhook_url"),
		username:   m.StringDefault("username", "syscheckr"),
		channel:    m.StringDefault("channel", ""),
		client:     &http.Client{Timeout: m.Duration("timeout", 15*time.Second)},
	}
	if err := m.Err(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *slackReporter) Name() string { return r.name }

type slackMessage struct {
	Username    string            `json:"username,omitempty"`
	Channel     string            `json:"channel,omitempty"`
	Text        string            `json:"text"`
	Attachments []slackAttachment `json:"attachments,omitempty"`
}

type slackAttachment struct {
	Color  string       `json:"color"`
	Title  string       `json:"title"`
	Text   string       `json:"text,omitempty"`
	Fields []slackField `json:"fields,omitempty"`
}

type slackField struct {
	Title string `json:"title"`
	Value string `json:"value"`
	Short bool   `json:"short"`
}

func (r *slackReporter) Report(ctx context.Context, results []check.Result) error {
	worst := check.StatusOK
	for _, res := range results {
		if res.Status.Severity() > worst.Severity() {
			worst = res.Status
		}
	}

	msg := slackMessage{
		Username: r.username,
		Channel:  r.channel,
		Text:     fmt.Sprintf("%s syscheckr: %d check(s) need attention (worst: %s)", emoji(worst), len(results), worst),
	}
	for _, res := range results {
		msg.Attachments = append(msg.Attachments, slackAttachment{
			Color:  color(res.Status),
			Title:  fmt.Sprintf("%s %s — %s", emoji(res.Status), res.Check, strings.ToUpper(res.Status.String())),
			Text:   res.Summary,
			Fields: detailFields(res),
		})
	}

	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal slack message: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.webhookURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("post slack: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("slack returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}

func detailFields(res check.Result) []slackField {
	if res.Error != "" {
		return []slackField{{Title: "error", Value: res.Error, Short: false}}
	}
	var fields []slackField
	for k, v := range res.Details {
		if k == "samples" || k == "output" {
			continue // too verbose for Slack fields
		}
		fields = append(fields, slackField{Title: k, Value: fmt.Sprint(v), Short: true})
	}
	return fields
}

func color(s check.Status) string {
	switch s {
	case check.StatusOK:
		return "good"
	case check.StatusWarn:
		return "warning"
	case check.StatusCrit:
		return "danger"
	default:
		return "#808080"
	}
}

func emoji(s check.Status) string {
	switch s {
	case check.StatusOK:
		return ":white_check_mark:"
	case check.StatusWarn:
		return ":warning:"
	case check.StatusCrit:
		return ":rotating_light:"
	default:
		return ":grey_question:"
	}
}
