package check

// Field describes one key in a check's (or reporter's) `config:` block: enough
// metadata for `syscheckr init` to prompt for it, `list-checks --describe` to
// document it, and both to emit a starter value. It mirrors the confutil calls
// in each factory by hand; specs_test.go guards against them drifting apart.
type Field struct {
	Key      string
	Required bool
	Default  any    // effective default; also seeded into starter configs when "meaningful"
	Prompt   string // wizard label / describe description; falls back to Key
}

// Specs lists the config fields for each registered check type. It MUST include
// every required field (the drift test builds a check from the spec and fails if
// one is missing). Types with no config block map to nil.
var Specs = map[string][]Field{
	"disk": {
		{Key: "path", Default: "/", Prompt: "mount point to inspect"},
		{Key: "warn_percent", Default: 80, Prompt: "warn at/above this used percent"},
		{Key: "crit_percent", Default: 90, Prompt: "crit at/above this used percent"},
	},
	"mount": {
		{Key: "path", Required: true, Prompt: "mount point that must be mounted"},
		{Key: "device", Prompt: "expected source device, e.g. /dev/sda1"},
		{Key: "fstype", Prompt: "expected filesystem type, e.g. ext4"},
		{Key: "read_only", Default: false, Prompt: "true if the mount is expected to be read-only"},
	},
	"cpu": {
		{Key: "sample", Default: "1s", Prompt: "sampling window, e.g. 1s"},
		{Key: "warn_percent", Default: 85, Prompt: "warn at/above this busy percent"},
		{Key: "crit_percent", Default: 95, Prompt: "crit at/above this busy percent"},
	},
	"memory": {
		{Key: "warn_percent", Default: 85, Prompt: "warn at/above this used percent"},
		{Key: "crit_percent", Default: 95, Prompt: "crit at/above this used percent"},
	},
	"docker_running": nil,
	"docker_container": {
		{Key: "name", Required: true, Prompt: "container name (without leading slash)"},
		{Key: "state", Default: "running", Prompt: "expected container state"},
		{Key: "healthy", Default: false, Prompt: "true to also require a (healthy) status"},
	},
	"log": {
		{Key: "path", Required: true, Prompt: "log file path"},
		{Key: "pattern", Required: true, Prompt: "regexp matched per line"},
		{Key: "window", Prompt: "only count matches newer than now-window, e.g. 5m"},
		{Key: "time_layout", Prompt: "Go time layout for the leading timestamp (default RFC3339)"},
		{Key: "warn_count", Default: 1, Prompt: "warn at/above this many matches"},
		{Key: "crit_count", Prompt: "crit at/above this many matches (0 disables)"},
		{Key: "max_lines", Default: 10000, Prompt: "cap lines read from the tail"},
	},
	"http": {
		{Key: "url", Required: true, Prompt: "URL to probe"},
		{Key: "method", Default: "GET", Prompt: "HTTP method"},
		{Key: "expect_status", Default: 200, Prompt: "required status code"},
		{Key: "warn_ms", Prompt: "warn at/above this latency in ms (0 disables)"},
		{Key: "crit_ms", Prompt: "crit at/above this latency in ms (0 disables)"},
		{Key: "headers", Prompt: "map of request headers"},
	},
	"command": {
		{Key: "command", Required: true, Prompt: "program to run (full command line when shell:true)"},
		{Key: "args", Prompt: "argument list (ignored when shell:true)"},
		{Key: "shell", Default: false, Prompt: "run via sh -c so pipes/globs work"},
		{Key: "pwd", Prompt: "working directory (default: process cwd)"},
		{Key: "expect_exit", Default: 0, Prompt: "exit code considered OK"},
		{Key: "match_pattern", Prompt: "regexp that MUST appear in output, else crit"},
		{Key: "warn_pattern", Prompt: "regexp that, if present, yields warn"},
		{Key: "crit_pattern", Prompt: "regexp that, if present, yields crit"},
	},
}
