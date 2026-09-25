// Package config loads the gospy configuration file: YAML with environment references,
// defaults for what the file leaves out, and the validation that turns a typo into a startup
// error instead of an empty profile.
package config

import (
	"errors"
	"fmt"
	"maps"
	"net/url"
	"regexp"
	"sort"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/hakastein/gospy/internal/phpspy"
	"github.com/hakastein/gospy/internal/procscan"
	"github.com/hakastein/gospy/internal/tag"
)

// Defaults for the settings the file may leave out.
const (
	DefaultPhpspy           = "phpspy"
	DefaultInstanceName     = "gospy"
	DefaultPyroscopeTimeout = 10 * time.Second
	DefaultPyroscopeWorkers = 5
	DefaultRateMB           = 4
	DefaultRateBurstMB      = 6
	DefaultBatchInterval    = 5 * time.Second
	DefaultStatsInterval    = 10 * time.Second
	DefaultDrainTimeout     = 10 * time.Second
	DefaultTargetRate       = 99
	DefaultRotate           = time.Minute
	DefaultScanInterval     = time.Second
	MinScanInterval         = 100 * time.Millisecond
)

// Bounds on what the runtime sizes itself by: a slot costs a live phpspy and a worker a
// goroutine, so a value past these is a typo, not a plan.
const (
	MaxTargetSlots      = 10000
	MaxPyroscopeWorkers = 1000
	// MaxCommLength is what the kernel keeps of a process name: a longer comm matcher can never match.
	MaxCommLength = 15
)

// AuthVariable is the only place the Pyroscope token is read from: the file can be baked
// into an image, the environment is where secrets live.
const AuthVariable = "GOSPY_PYROSCOPE_AUTH"

// Config is the typed configuration the runtime takes.
type Config struct {
	Pyroscope     Pyroscope
	App           string
	Tags          map[string]string
	Phpspy        string
	BatchInterval time.Duration
	StatsInterval time.Duration
	DrainTimeout  time.Duration
	InstanceName  string
	Targets       []Target
}

// Pyroscope is where and how batches are shipped.
type Pyroscope struct {
	URL         string
	Auth        string
	Timeout     time.Duration
	Workers     int
	RateMB      float64
	RateBurstMB float64
}

// Target is one named group of processes with its own slots, rate and tags.
type Target struct {
	Name         string
	Match        Matcher
	MaxProcesses int
	Rate         int
	Rotate       time.Duration
	ScanInterval time.Duration
	// Tags are the target's tags with the global ones underneath, as key=value lines.
	Tags []string
	// Parser is how the target's phpspy output turns into samples: its static tag set, the
	// dynamic tags read per request and the entry-point rules. The attach sets the rate.
	Parser     phpspy.ParserConfig
	PhpspyArgs []string
}

// Enabled is false for a target whose slot count is zero: it is skipped at startup.
func (target Target) Enabled() bool {
	return target.MaxProcesses > 0
}

// Matcher selects processes; every field set must hold, and a match on Exclude rejects.
type Matcher struct {
	Comm    string
	Cmdline *regexp.Regexp
	Exe     string
	UID     *int
	Exclude *regexp.Regexp
}

// Matches reports whether process belongs to the target.
func (matcher Matcher) Matches(process procscan.Process) bool {
	if matcher.Comm != "" && process.Comm != matcher.Comm {
		return false
	}
	if matcher.Exe != "" && process.Exe != matcher.Exe {
		return false
	}
	if matcher.UID != nil && process.UID != *matcher.UID {
		return false
	}
	if matcher.Cmdline != nil && !matcher.Cmdline.MatchString(process.Cmdline) {
		return false
	}
	if matcher.Exclude != nil && matcher.Exclude.MatchString(process.Cmdline) {
		return false
	}

	return true
}

// ScanFields is what the scanner has to read for the matchers to work. A disabled target
// still claims the processes it matches, so its matchers count too.
func (cfg Config) ScanFields() procscan.Fields {
	var fields procscan.Fields
	for _, target := range cfg.Targets {
		if target.Match.UID != nil {
			fields |= procscan.FieldUID
		}
		if target.Match.Exe != "" {
			fields |= procscan.FieldExe
		}
	}

	return fields
}

// Load reads the file content with lookup as the environment. The warnings are for the
// operator to see at startup; a configuration that returns an error must not be run.
func Load(content []byte, lookup LookupFunc) (Config, []string, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(content, &root); err != nil {
		return Config{}, nil, fmt.Errorf("cannot parse configuration: %w", err)
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return Config{}, nil, errors.New("configuration is empty")
	}

	if err := expandValues(&root, lookup); err != nil {
		return Config{}, nil, err
	}

	cfg, err := parse(node{Node: root.Content[0]})
	if err != nil {
		return Config{}, nil, err
	}

	cfg.Pyroscope.Auth, _ = lookup(AuthVariable)

	if err := validate(cfg); err != nil {
		return Config{}, nil, err
	}

	return cfg, warnings(cfg), nil
}

func parse(doc node) (Config, error) {
	cfg := Config{
		Pyroscope: Pyroscope{
			Timeout:     DefaultPyroscopeTimeout,
			Workers:     DefaultPyroscopeWorkers,
			RateMB:      DefaultRateMB,
			RateBurstMB: DefaultRateBurstMB,
		},
		Phpspy:        DefaultPhpspy,
		BatchInterval: DefaultBatchInterval,
		StatsInterval: DefaultStatsInterval,
		DrainTimeout:  DefaultDrainTimeout,
		InstanceName:  DefaultInstanceName,
	}

	pairs, err := doc.mapping()
	if err != nil {
		return Config{}, err
	}

	var (
		targets     []node
		haveTargets bool
	)
	for _, pair := range pairs {
		switch pair.key {
		case "pyroscope":
			err = parsePyroscope(pair.value, &cfg.Pyroscope)
		case "app":
			cfg.App, err = pair.value.text()
		case "tags":
			cfg.Tags, err = parseTags(pair.value)
		case "phpspy":
			cfg.Phpspy, err = pair.value.text()
		case "batch-interval":
			cfg.BatchInterval, err = pair.value.duration()
		case "stats-interval":
			cfg.StatsInterval, err = pair.value.duration()
		case "drain-timeout":
			cfg.DrainTimeout, err = pair.value.duration()
		case "instance-name":
			cfg.InstanceName, err = pair.value.text()
		case "targets":
			targets, err = pair.value.sequence()
			haveTargets = true
		default:
			err = pair.value.errorf("unknown key")
		}
		if err != nil {
			return Config{}, err
		}
	}

	if !haveTargets {
		return Config{}, doc.errorf("targets is required")
	}

	for _, item := range targets {
		target, err := parseTarget(item, cfg.Tags)
		if err != nil {
			return Config{}, err
		}

		cfg.Targets = append(cfg.Targets, target)
	}

	return cfg, nil
}

func parsePyroscope(n node, pyroscope *Pyroscope) error {
	pairs, err := n.mapping()
	if err != nil {
		return err
	}

	for _, pair := range pairs {
		switch pair.key {
		case "url":
			pyroscope.URL, err = pair.value.text()
		case "timeout":
			pyroscope.Timeout, err = pair.value.duration()
		case "workers":
			pyroscope.Workers, err = pair.value.integer()
		case "rate-mb":
			pyroscope.RateMB, err = pair.value.number()
		case "rate-burst-mb":
			pyroscope.RateBurstMB, err = pair.value.number()
		case "auth", "token":
			err = pair.value.errorf("the token is not a file setting: set %s in the environment", AuthVariable)
		default:
			err = pair.value.errorf("unknown key")
		}
		if err != nil {
			return err
		}
	}

	return nil
}

func parseTags(n node) (map[string]string, error) {
	pairs, err := n.mapping()
	if err != nil {
		return nil, err
	}

	tags := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		value, err := pair.value.text()
		if err != nil {
			return nil, err
		}

		tags[pair.key] = value
	}

	return tags, nil
}

func parseTarget(n node, globalTags map[string]string) (Target, error) {
	target := Target{
		Rate:         DefaultTargetRate,
		Rotate:       DefaultRotate,
		ScanInterval: DefaultScanInterval,
		Parser:       phpspy.ParserConfig{KeepEntrypointName: true},
		MaxProcesses: -1,
	}

	pairs, err := n.mapping()
	if err != nil {
		return Target{}, err
	}

	var (
		tags      map[string]string
		haveMatch bool
	)
	for _, pair := range pairs {
		switch pair.key {
		case "name":
			target.Name, err = pair.value.text()
		case "match":
			target.Match, err = parseMatcher(pair.value)
			haveMatch = true
		case "max-processes":
			target.MaxProcesses, err = pair.value.integer()
		case "rate":
			target.Rate, err = pair.value.integer()
		case "rotate":
			target.Rotate, err = pair.value.duration()
		case "scan-interval":
			target.ScanInterval, err = pair.value.duration()
		case "tags":
			tags, err = parseTags(pair.value)
		case "entrypoints":
			target.Parser.Entrypoints, err = pair.value.strings()
		case "tag-entrypoint":
			target.Parser.TagEntrypoint, err = pair.value.boolean()
		case "keep-entrypoint-name":
			target.Parser.KeepEntrypointName, err = pair.value.boolean()
		case "phpspy-args":
			target.PhpspyArgs, err = pair.value.strings()
		default:
			err = pair.value.errorf("unknown key")
		}
		if err != nil {
			return Target{}, err
		}
	}

	if target.Name == "" {
		return Target{}, n.errorf("name is required")
	}
	if !validTargetName(target.Name) {
		return Target{}, n.errorf("name %q may only contain letters, digits, '_', '-' and '.'", target.Name)
	}
	if !haveMatch {
		return Target{}, n.errorf("target %q: match is required", target.Name)
	}
	if target.MaxProcesses < 0 {
		return Target{}, n.errorf("target %q: max-processes is required and cannot be negative", target.Name)
	}
	if target.Rate < 1 {
		return Target{}, n.errorf("target %q: rate must be at least 1 Hz, got %d", target.Name, target.Rate)
	}
	if target.MaxProcesses > MaxTargetSlots {
		return Target{}, n.errorf("target %q: max-processes cannot exceed %d, got %d", target.Name, MaxTargetSlots, target.MaxProcesses)
	}
	if target.ScanInterval < MinScanInterval {
		return Target{}, n.errorf("target %q: scan-interval must be at least %s, got %s", target.Name, MinScanInterval, target.ScanInterval)
	}
	if err := phpspy.ValidateArgs(target.PhpspyArgs); err != nil {
		return Target{}, n.errorf("target %q: phpspy-args: %s", target.Name, err)
	}

	target.Tags = mergeTags(globalTags, tags)
	target.Parser.StaticTags, target.Parser.DynamicTags, err = tag.ParseInput(target.Tags)
	if err != nil {
		return Target{}, n.errorf("target %q: tags: %s", target.Name, err)
	}

	return target, nil
}

func parseMatcher(n node) (Matcher, error) {
	pairs, err := n.mapping()
	if err != nil {
		return Matcher{}, err
	}
	if len(pairs) == 0 {
		return Matcher{}, n.errorf("at least one matcher is required")
	}

	var matcher Matcher
	for _, pair := range pairs {
		switch pair.key {
		case "comm":
			matcher.Comm, err = matcherText(pair.value)
			if err == nil && len(matcher.Comm) > MaxCommLength {
				err = pair.value.errorf("comm %q is longer than the %d characters the kernel keeps of a process name", matcher.Comm, MaxCommLength)
			}
		case "cmdline":
			matcher.Cmdline, err = parseRegexp(pair.value)
		case "exe":
			matcher.Exe, err = matcherText(pair.value)
		case "uid":
			var uid int
			uid, err = pair.value.integer()
			matcher.UID = &uid
		case "exclude":
			matcher.Exclude, err = parseRegexp(pair.value)
		default:
			err = pair.value.errorf("unknown matcher")
		}
		if err != nil {
			return Matcher{}, err
		}
	}

	if matcher.Comm == "" && matcher.Cmdline == nil && matcher.Exe == "" && matcher.UID == nil {
		return Matcher{}, n.errorf("exclude alone selects nothing: add comm, cmdline, exe or uid")
	}

	return matcher, nil
}

// matcherText reads a matcher value that has to say something: an empty or null one would
// silently match nothing, or everything.
func matcherText(n node) (string, error) {
	text, err := n.text()
	if err != nil {
		return "", err
	}
	if text == "" {
		return "", n.errorf("a matcher cannot be empty")
	}

	return text, nil
}

func parseRegexp(n node) (*regexp.Regexp, error) {
	text, err := matcherText(n)
	if err != nil {
		return nil, err
	}

	compiled, err := regexp.Compile(text)
	if err != nil {
		return nil, n.errorf("invalid regex: %s", err)
	}

	return compiled, nil
}

// mergeTags lays the target's tags over the global ones and renders them as key=value lines.
func mergeTags(global, own map[string]string) []string {
	merged := make(map[string]string, len(global)+len(own))
	maps.Copy(merged, global)
	maps.Copy(merged, own)

	lines := make([]string, 0, len(merged))
	for key, value := range merged {
		lines = append(lines, key+"="+value)
	}
	sort.Strings(lines)

	return lines
}

func validTargetName(name string) bool {
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-', r == '.':
		default:
			return false
		}
	}

	return true
}

func validate(cfg Config) error {
	if cfg.App == "" {
		return errors.New("app is required")
	}
	if cfg.Phpspy == "" {
		return errors.New("phpspy cannot be empty")
	}
	if cfg.BatchInterval <= 0 {
		return errors.New("batch-interval must be above zero")
	}
	if cfg.DrainTimeout <= 0 {
		return errors.New("drain-timeout must be above zero")
	}

	if err := validatePyroscope(cfg.Pyroscope); err != nil {
		return err
	}

	if len(cfg.Targets) == 0 {
		return errors.New("targets: at least one target is required")
	}

	names := make(map[string]struct{}, len(cfg.Targets))
	for _, target := range cfg.Targets {
		if _, duplicate := names[target.Name]; duplicate {
			return fmt.Errorf("targets: name %q is used twice", target.Name)
		}
		names[target.Name] = struct{}{}
	}

	return nil
}

func validatePyroscope(pyroscope Pyroscope) error {
	if pyroscope.URL == "" {
		return errors.New("pyroscope.url is required")
	}

	parsed, err := url.Parse(pyroscope.URL)
	if err != nil {
		return fmt.Errorf("pyroscope.url %q is invalid: %w", pyroscope.URL, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("pyroscope.url must be http or https, got %q", pyroscope.URL)
	}

	if pyroscope.Timeout <= 0 {
		return errors.New("pyroscope.timeout must be above zero")
	}
	if pyroscope.Workers < 1 {
		return fmt.Errorf("pyroscope.workers must be at least 1, got %d", pyroscope.Workers)
	}
	if pyroscope.Workers > MaxPyroscopeWorkers {
		return fmt.Errorf("pyroscope.workers cannot exceed %d, got %d", MaxPyroscopeWorkers, pyroscope.Workers)
	}
	if pyroscope.RateMB < 0 {
		return fmt.Errorf("pyroscope.rate-mb must not be negative, got %v", pyroscope.RateMB)
	}
	if pyroscope.RateBurstMB < 0 {
		return fmt.Errorf("pyroscope.rate-burst-mb must not be negative, got %v", pyroscope.RateBurstMB)
	}
	if pyroscope.RateMB > 0 && pyroscope.RateBurstMB == 0 {
		return errors.New("pyroscope.rate-burst-mb must be above zero when rate-mb is set")
	}

	return nil
}

// warnings are what the operator should hear at startup without being stopped: a token that
// travels in cleartext, and enabled targets whose static tag sets are identical, which
// Pyroscope would merge into one series.
func warnings(cfg Config) []string {
	var warnings []string

	if cfg.Pyroscope.Auth != "" {
		if parsed, err := url.Parse(cfg.Pyroscope.URL); err == nil && parsed.Scheme == "http" {
			warnings = append(warnings, fmt.Sprintf("pyroscope.url %s is plain http: the authentication token travels in cleartext", cfg.Pyroscope.URL))
		}
	}

	seen := make(map[string]string, len(cfg.Targets))
	for _, target := range cfg.Targets {
		if !target.Enabled() {
			continue
		}

		tags := target.Parser.StaticTags
		if other, found := seen[tags]; found {
			warnings = append(warnings, fmt.Sprintf(
				"targets %q and %q carry the same static tags {%s}: their samples merge into one series",
				other, target.Name, tags,
			))
			continue
		}
		seen[tags] = target.Name
	}

	return warnings
}
