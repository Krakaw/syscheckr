package report

import "github.com/Krakaw/syscheckr/internal/check"

// Specs lists the config fields for each registered reporter type, in the same
// shape as check.Specs. It MUST include every required field; the drift test
// builds a reporter from the spec and fails if one is missing.
var Specs = map[string][]check.Field{
	"log": {
		{Key: "format", Default: "text", Prompt: "text or json"},
		{Key: "output", Default: "stdout", Prompt: "stdout, stderr, or a file path"},
	},
	"slack": {
		{Key: "webhook_url", Required: true, Prompt: "Slack incoming webhook URL"},
		{Key: "username", Default: "syscheckr", Prompt: "override the bot username"},
		{Key: "channel", Prompt: "override the destination channel"},
		{Key: "timeout", Default: "15s", Prompt: "request timeout"},
	},
	"webhook": {
		{Key: "url", Required: true, Prompt: "destination URL"},
		{Key: "headers", Prompt: "map of extra request headers"},
		{Key: "secret", Prompt: "if set, HMAC-SHA256 sign the body"},
		{Key: "redact", Default: false, Prompt: "strip log samples / command output from details"},
		{Key: "timeout", Default: "15s", Prompt: "request timeout"},
	},
	"linear": {
		{Key: "api_key", Required: true, Prompt: "Linear API key"},
		{Key: "team_id", Required: true, Prompt: "Linear team ID"},
		{Key: "redact", Default: false, Prompt: "strip log samples / command output from the issue"},
		{Key: "timeout", Default: "15s", Prompt: "request timeout"},
		{Key: "api_url", Prompt: "override the Linear API URL"},
		{Key: "label_ids", Prompt: "list of Linear label IDs to attach"},
	},
}
