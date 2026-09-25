package config_test

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/hakastein/gospy/internal/config"
	"github.com/hakastein/gospy/internal/phpspy"
	"github.com/hakastein/gospy/internal/procscan"
	"github.com/hakastein/gospy/internal/tag"
)

const minimal = `
pyroscope:
  url: http://pyroscope.test
app: checkout
targets:
  - name: fpm
    match: { comm: php-fpm }
    max-processes: 5
`

func env(pairs map[string]string) config.LookupFunc {
	return func(name string) (string, bool) {
		value, ok := pairs[name]
		return value, ok
	}
}

func load(t *testing.T, content string, pairs map[string]string) (config.Config, []string) {
	t.Helper()

	cfg, warnings, err := config.Load([]byte(content), env(pairs))
	require.NoError(t, err)

	return cfg, warnings
}

func intPtr(value int) *int {
	return &value
}

func TestLoadAppliesEveryDefault(t *testing.T) {
	t.Parallel()

	cfg, warnings := load(t, minimal, nil)

	require.Empty(t, warnings)
	require.Equal(t, config.Pyroscope{
		URL:         "http://pyroscope.test",
		Timeout:     10 * time.Second,
		Workers:     5,
		RateMB:      4,
		RateBurstMB: 6,
	}, cfg.Pyroscope)
	require.Equal(t, "checkout", cfg.App)
	require.Empty(t, cfg.Tags)
	require.Equal(t, "phpspy", cfg.Phpspy)
	require.Equal(t, 5*time.Second, cfg.BatchInterval)
	require.Equal(t, 10*time.Second, cfg.StatsInterval)
	require.Equal(t, 10*time.Second, cfg.DrainTimeout)
	require.Equal(t, "gospy", cfg.InstanceName)

	require.Len(t, cfg.Targets, 1)
	target := cfg.Targets[0]
	require.Equal(t, "fpm", target.Name)
	require.Equal(t, config.Matcher{Comm: "php-fpm"}, target.Match)
	require.Equal(t, 5, target.MaxProcesses)
	require.True(t, target.Enabled())
	require.Equal(t, 99, target.Rate)
	require.Equal(t, time.Minute, target.Rotate)
	require.Equal(t, time.Second, target.ScanInterval)
	require.Empty(t, target.Tags)
	require.Equal(t, phpspy.ParserConfig{KeepEntrypointName: true, DynamicTags: map[string][]tag.DynamicTag{}}, target.Parser)
	require.Empty(t, target.PhpspyArgs)
}

func TestLoadReadsEverySetting(t *testing.T) {
	t.Parallel()

	const content = `
pyroscope:
  url: https://pyroscope.test/base?tenant=a
  timeout: 3s
  workers: 2
  rate-mb: 1.5
  rate-burst-mb: 2
app: macro
tags:
  env: production
  host: web-01
phpspy: /usr/local/bin/phpspy
batch-interval: 2s
stats-interval: 0
drain-timeout: 4s
instance-name: gospy-fpm
targets:
  - name: fpm
    match:
      comm: php-fpm
      cmdline: '^php-fpm: pool '
      exe: /usr/local/sbin/php-fpm
      uid: 33
      exclude: 'pool admin'
    max-processes: 8
    rate: 25
    rotate: 30s
    scan-interval: 500ms
    tags:
      source: fpm
      host: web-02
      uri: '{{ "glopeek server.REQUEST_URI" "^([^?]+)\\?.*$" "$1" }}'
    entrypoints: [index.php, '/srv/bin/**/*.php']
    tag-entrypoint: true
    keep-entrypoint-name: false
    phpspy-args: [--max-depth=-1, --php-version=74, -c]
  - name: cron
    match: { comm: php, cmdline: '/cronjobs/' }
    max-processes: 0
    rotate: 0
    tags: { source: cron }
`

	cfg, warnings := load(t, content, map[string]string{"GOSPY_PYROSCOPE_AUTH": "secret"})

	require.Empty(t, warnings)
	require.Equal(t, config.Pyroscope{
		URL:         "https://pyroscope.test/base?tenant=a",
		Auth:        "secret",
		Timeout:     3 * time.Second,
		Workers:     2,
		RateMB:      1.5,
		RateBurstMB: 2,
	}, cfg.Pyroscope)
	require.Equal(t, "macro", cfg.App)
	require.Equal(t, map[string]string{"env": "production", "host": "web-01"}, cfg.Tags)
	require.Equal(t, "/usr/local/bin/phpspy", cfg.Phpspy)
	require.Equal(t, 2*time.Second, cfg.BatchInterval)
	require.Zero(t, cfg.StatsInterval)
	require.Equal(t, 4*time.Second, cfg.DrainTimeout)
	require.Equal(t, "gospy-fpm", cfg.InstanceName)

	require.Len(t, cfg.Targets, 2)

	fpm := cfg.Targets[0]
	require.Equal(t, "fpm", fpm.Name)
	require.Equal(t, "php-fpm", fpm.Match.Comm)
	require.Equal(t, "^php-fpm: pool ", fpm.Match.Cmdline.String())
	require.Equal(t, "/usr/local/sbin/php-fpm", fpm.Match.Exe)
	require.Equal(t, intPtr(33), fpm.Match.UID)
	require.Equal(t, "pool admin", fpm.Match.Exclude.String())
	require.Equal(t, 8, fpm.MaxProcesses)
	require.Equal(t, 25, fpm.Rate)
	require.Equal(t, 30*time.Second, fpm.Rotate)
	require.Equal(t, 500*time.Millisecond, fpm.ScanInterval)
	require.Equal(t, []string{"env=production", "host=web-02", "source=fpm", `uri={{ "glopeek server.REQUEST_URI" "^([^?]+)\\?.*$" "$1" }}`}, fpm.Tags, "target tags lie over the global ones")
	require.Equal(t, "env=production,host=web-02,source=fpm", fpm.Parser.StaticTags)
	require.Len(t, fpm.Parser.DynamicTags["glopeek server.REQUEST_URI"], 1)
	uri := fpm.Parser.DynamicTags["glopeek server.REQUEST_URI"][0]
	require.Equal(t, "uri", uri.TagKey)
	require.Equal(t, `^([^?]+)\?.*$`, uri.TagRegexp.String())
	require.Equal(t, "$1", uri.TagReplace)
	require.Equal(t, []string{"index.php", "/srv/bin/**/*.php"}, fpm.Parser.Entrypoints)
	require.True(t, fpm.Parser.TagEntrypoint)
	require.False(t, fpm.Parser.KeepEntrypointName)
	require.Zero(t, fpm.Parser.SampleRate, "the attach sets the rate")
	require.Equal(t, []string{"--max-depth=-1", "--php-version=74", "-c"}, fpm.PhpspyArgs)

	cron := cfg.Targets[1]
	require.Equal(t, "cron", cron.Name)
	require.False(t, cron.Enabled())
	require.Zero(t, cron.Rotate)
	require.Equal(t, "env=production,host=web-01,source=cron", cron.Parser.StaticTags)

	require.Equal(t, procscan.FieldUID|procscan.FieldExe, cfg.ScanFields())
}

func TestLoadExpandsTheEnvironment(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		content string
		env     map[string]string
		want    func(t *testing.T, cfg config.Config)
		wantErr string
	}{
		{
			name: "a set variable",
			content: `
pyroscope: { url: "${PYROSCOPE_URL}" }
app: checkout
targets: [{ name: fpm, match: { comm: php-fpm }, max-processes: "${SLOTS}" }]
`,
			env: map[string]string{"PYROSCOPE_URL": "http://pyroscope.test", "SLOTS": "7"},
			want: func(t *testing.T, cfg config.Config) {
				require.Equal(t, "http://pyroscope.test", cfg.Pyroscope.URL)
				require.Equal(t, 7, cfg.Targets[0].MaxProcesses, "an expanded value is typed by the schema, not by its quoting")
			},
		},
		{
			name: "a default for an unset variable",
			content: `
pyroscope: { url: http://pyroscope.test }
app: checkout
targets:
  - name: fpm
    match: { comm: php-fpm }
    max-processes: ${SLOTS:-5}
    rate: ${RATE:-10}
`,
			want: func(t *testing.T, cfg config.Config) {
				require.Equal(t, 5, cfg.Targets[0].MaxProcesses, "a plain reference works unquoted in block style")
				require.Equal(t, 10, cfg.Targets[0].Rate)
			},
		},
		{
			name: "a default for an empty variable",
			content: `
pyroscope: { url: http://pyroscope.test }
app: checkout
targets: [{ name: fpm, match: { comm: php-fpm }, max-processes: "${SLOTS:-5}" }]
`,
			env: map[string]string{"SLOTS": ""},
			want: func(t *testing.T, cfg config.Config) {
				require.Equal(t, 5, cfg.Targets[0].MaxProcesses)
			},
		},
		{
			name: "a set variable wins over its default",
			content: `
pyroscope: { url: http://pyroscope.test }
app: ${APP:-fallback}
targets: [{ name: fpm, match: { comm: php-fpm }, max-processes: 1 }]
`,
			env: map[string]string{"APP": "checkout"},
			want: func(t *testing.T, cfg config.Config) {
				require.Equal(t, "checkout", cfg.App)
			},
		},
		{
			name: "references inside a longer value",
			content: `
pyroscope: { url: "https://${HOST}:${PORT:-4040}/pyroscope" }
app: checkout
targets: [{ name: fpm, match: { comm: php-fpm }, max-processes: 1 }]
`,
			env: map[string]string{"HOST": "pyroscope.test"},
			want: func(t *testing.T, cfg config.Config) {
				require.Equal(t, "https://pyroscope.test:4040/pyroscope", cfg.Pyroscope.URL)
			},
		},
		{
			name: "a doubled dollar sign is a literal one and a lone one stays",
			content: `
pyroscope: { url: http://pyroscope.test }
app: checkout
tags: { price: 'a$$b', literal: '$$', route: '{{ "uri" "^/orders/[0-9]+$" "/orders/$1" }}' }
targets: [{ name: fpm, match: { comm: php-fpm }, max-processes: 1 }]
`,
			want: func(t *testing.T, cfg config.Config) {
				require.Equal(t, "a$b", cfg.Tags["price"])
				require.Equal(t, "$", cfg.Tags["literal"])
				require.Equal(t, "/orders/$1", cfg.Targets[0].Parser.DynamicTags["uri"][0].TagReplace, "the $1 of a rewrite is not a reference")
			},
		},
		{
			name: "a dynamic tag is left to the regexp engine",
			content: `
pyroscope: { url: http://pyroscope.test }
app: checkout
tags:
  host: ${HOST}
  route: '{{ "uri" "^/(?P<seg>[a-z]+)/.*$" "/${seg}/$$" }}'
targets:
  - name: fpm
    match: { comm: php-fpm }
    max-processes: 1
    tags:
      uri: '{{ "uri" "^([^?]+)\\?(?P<query>.*)$" "$1?${query}" }}'
`,
			env: map[string]string{"HOST": "web-01"},
			want: func(t *testing.T, cfg config.Config) {
				require.Equal(t, "web-01", cfg.Tags["host"], "a static tag is expanded")
				dynamic := cfg.Targets[0].Parser.DynamicTags["uri"]
				require.Len(t, dynamic, 2)
				replacements := []string{dynamic[0].TagReplace, dynamic[1].TagReplace}
				require.ElementsMatch(t, []string{"/${seg}/$$", "$1?${query}"}, replacements, "capture group references inside a dynamic tag are not environment references")
			},
		},
		{
			name: "the token comes from the environment only",
			content: `
pyroscope: { url: http://pyroscope.test }
app: checkout
targets: [{ name: fpm, match: { comm: php-fpm }, max-processes: 1 }]
`,
			env: map[string]string{"GOSPY_PYROSCOPE_AUTH": "secret-token"},
			want: func(t *testing.T, cfg config.Config) {
				require.Equal(t, "secret-token", cfg.Pyroscope.Auth)
			},
		},
		{
			name: "an unset variable without a default",
			content: `
pyroscope: { url: "${PYROSCOPE_URL}" }
app: checkout
targets: [{ name: fpm, match: { comm: php-fpm }, max-processes: 1 }]
`,
			wantErr: "environment variable PYROSCOPE_URL is not set and has no default",
		},
		{
			name: "an unterminated reference",
			content: `
pyroscope: { url: "${PYROSCOPE_URL" }
app: checkout
targets: [{ name: fpm, match: { comm: php-fpm }, max-processes: 1 }]
`,
			wantErr: "unterminated reference",
		},
		{
			name: "a malformed reference",
			content: `
pyroscope: { url: "${PYROSCOPE URL}" }
app: checkout
targets: [{ name: fpm, match: { comm: php-fpm }, max-processes: 1 }]
`,
			wantErr: "malformed reference ${PYROSCOPE URL}",
		},
		{
			name: "an expanded value that does not fit its type",
			content: `
pyroscope: { url: http://pyroscope.test }
app: checkout
targets: [{ name: fpm, match: { comm: php-fpm }, max-processes: "${SLOTS}" }]
`,
			env:     map[string]string{"SLOTS": "many"},
			wantErr: `targets[0].max-processes: expected an integer, got "many"`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg, _, err := config.Load([]byte(tc.content), env(tc.env))
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}

			require.NoError(t, err)
			tc.want(t, cfg)
		})
	}
}

func TestLoadRejectsABrokenConfiguration(t *testing.T) {
	t.Parallel()

	target := func(fields string) string {
		return "pyroscope: { url: http://pyroscope.test }\napp: checkout\ntargets:\n  - name: fpm\n    match: { comm: php-fpm }\n    max-processes: 1\n" + fields
	}

	testCases := []struct {
		name    string
		content string
		wantErr string
	}{
		{
			name:    "empty file",
			content: "# nothing here\n",
			wantErr: "configuration is empty",
		},
		{
			name:    "text that is not yaml",
			content: "pyroscope: [\n",
			wantErr: "cannot parse configuration",
		},
		{
			name:    "a list at the top",
			content: "- pyroscope\n",
			wantErr: "expected a mapping",
		},
		{
			name:    "unknown top-level key",
			content: minimal + "restart: always\n",
			wantErr: "line 9: restart: unknown key",
		},
		{
			name:    "unknown pyroscope key",
			content: "pyroscope: { url: http://pyroscope.test, retries: 3 }\napp: checkout\ntargets: [{ name: fpm, match: { comm: php-fpm }, max-processes: 1 }]\n",
			wantErr: "pyroscope.retries: unknown key",
		},
		{
			name:    "the token in the file",
			content: "pyroscope: { url: http://pyroscope.test, auth: secret }\napp: checkout\ntargets: [{ name: fpm, match: { comm: php-fpm }, max-processes: 1 }]\n",
			wantErr: "pyroscope.auth: the token is not a file setting: set GOSPY_PYROSCOPE_AUTH in the environment",
		},
		{
			name:    "unknown target key",
			content: target("    rotat: 30s\n"),
			wantErr: "line 7: targets[0].rotat: unknown key",
		},
		{
			name:    "unknown matcher",
			content: "pyroscope: { url: http://pyroscope.test }\napp: checkout\ntargets: [{ name: fpm, match: { command: php-fpm }, max-processes: 1 }]\n",
			wantErr: "targets[0].match.command: unknown matcher",
		},
		{
			name:    "a key given twice",
			content: minimal + "app: billing\n",
			wantErr: "app: key given twice",
		},
		{
			name:    "missing pyroscope url",
			content: "pyroscope: { workers: 2 }\napp: checkout\ntargets: [{ name: fpm, match: { comm: php-fpm }, max-processes: 1 }]\n",
			wantErr: "pyroscope.url is required",
		},
		{
			name:    "pyroscope url without a scheme",
			content: "pyroscope: { url: pyroscope.test:4040 }\napp: checkout\ntargets: [{ name: fpm, match: { comm: php-fpm }, max-processes: 1 }]\n",
			wantErr: "pyroscope.url must be http or https",
		},
		{
			name:    "pyroscope url that does not parse",
			content: "pyroscope: { url: 'http://pyroscope.test:port' }\napp: checkout\ntargets: [{ name: fpm, match: { comm: php-fpm }, max-processes: 1 }]\n",
			wantErr: "pyroscope.url \"http://pyroscope.test:port\" is invalid",
		},
		{
			name:    "zero pyroscope workers",
			content: "pyroscope: { url: http://pyroscope.test, workers: 0 }\napp: checkout\ntargets: [{ name: fpm, match: { comm: php-fpm }, max-processes: 1 }]\n",
			wantErr: "pyroscope.workers must be at least 1, got 0",
		},
		{
			name:    "a negative rate limit",
			content: "pyroscope: { url: http://pyroscope.test, rate-mb: -1 }\napp: checkout\ntargets: [{ name: fpm, match: { comm: php-fpm }, max-processes: 1 }]\n",
			wantErr: "pyroscope.rate-mb must not be negative",
		},
		{
			name:    "a negative burst",
			content: "pyroscope: { url: http://pyroscope.test, rate-burst-mb: -1 }\napp: checkout\ntargets: [{ name: fpm, match: { comm: php-fpm }, max-processes: 1 }]\n",
			wantErr: "pyroscope.rate-burst-mb must not be negative",
		},
		{
			name:    "a rate limit without a burst",
			content: "pyroscope: { url: http://pyroscope.test, rate-mb: 1, rate-burst-mb: 0 }\napp: checkout\ntargets: [{ name: fpm, match: { comm: php-fpm }, max-processes: 1 }]\n",
			wantErr: "pyroscope.rate-burst-mb must be above zero when rate-mb is set",
		},
		{
			name:    "a zero pyroscope timeout",
			content: "pyroscope: { url: http://pyroscope.test, timeout: 0 }\napp: checkout\ntargets: [{ name: fpm, match: { comm: php-fpm }, max-processes: 1 }]\n",
			wantErr: "pyroscope.timeout must be above zero",
		},
		{
			name:    "a bare number as a duration",
			content: "pyroscope: { url: http://pyroscope.test, timeout: 10 }\napp: checkout\ntargets: [{ name: fpm, match: { comm: php-fpm }, max-processes: 1 }]\n",
			wantErr: `pyroscope.timeout: expected a duration such as 10s or 500ms, got "10"`,
		},
		{
			name:    "a negative duration",
			content: minimal + "batch-interval: -5s\n",
			wantErr: `batch-interval: expected a duration of zero or more, got "-5s"`,
		},
		{
			name:    "a number where a boolean is expected",
			content: target("    tag-entrypoint: 1\n"),
			wantErr: `targets[0].tag-entrypoint: expected true or false, got "1"`,
		},
		{
			name:    "a list where a value is expected",
			content: "pyroscope: { url: http://pyroscope.test }\napp: [a, b]\ntargets: [{ name: fpm, match: { comm: php-fpm }, max-processes: 1 }]\n",
			wantErr: "app: expected a single value",
		},
		{
			name:    "a null matcher",
			content: "pyroscope: { url: http://pyroscope.test }\napp: checkout\ntargets: [{ name: fpm, match: { comm: ~, cmdline: pool }, max-processes: 1 }]\n",
			wantErr: "targets[0].match.comm: a matcher cannot be empty",
		},
		{
			name:    "an empty regex matcher",
			content: "pyroscope: { url: http://pyroscope.test }\napp: checkout\ntargets: [{ name: fpm, match: { cmdline: '' }, max-processes: 1 }]\n",
			wantErr: "targets[0].match.cmdline: a matcher cannot be empty",
		},
		{
			name:    "a comm longer than the kernel keeps",
			content: "pyroscope: { url: http://pyroscope.test }\napp: checkout\ntargets: [{ name: fpm, match: { comm: /usr/sbin/php-fpm }, max-processes: 1 }]\n",
			wantErr: `targets[0].match.comm: comm "/usr/sbin/php-fpm" is longer than the 15 characters the kernel keeps of a process name`,
		},
		{
			name:    "a zero batch interval",
			content: minimal + "batch-interval: 0\n",
			wantErr: "batch-interval must be above zero",
		},
		{
			name:    "a zero drain timeout",
			content: minimal + "drain-timeout: 0s\n",
			wantErr: "drain-timeout must be above zero",
		},
		{
			name:    "more slots than a host can hold",
			content: "pyroscope: { url: http://pyroscope.test }\napp: checkout\ntargets: [{ name: fpm, match: { comm: php-fpm }, max-processes: 2000000000 }]\n",
			wantErr: `target "fpm": max-processes cannot exceed 10000, got 2000000000`,
		},
		{
			name:    "more pyroscope workers than sensible",
			content: "pyroscope: { url: http://pyroscope.test, workers: 1000000 }\napp: checkout\ntargets: [{ name: fpm, match: { comm: php-fpm }, max-processes: 1 }]\n",
			wantErr: "pyroscope.workers cannot exceed 1000, got 1000000",
		},
		{
			name:    "a bare word in phpspy-args",
			content: target("    phpspy-args: [-c, php]\n"),
			wantErr: `target "fpm": phpspy-args: phpspy argument "php" is not a flag`,
		},
		{
			name:    "an abbreviated managed phpspy flag",
			content: target("    phpspy-args: [--rate=250]\n"),
			wantErr: `target "fpm": phpspy-args: phpspy flag -H/--rate-hz is managed by gospy and cannot be passed`,
		},
		{
			name:    "a mapping where a value is expected",
			content: target("    rate: { hz: 10 }\n"),
			wantErr: "targets[0].rate: expected a single value",
		},
		{
			name:    "missing app",
			content: "pyroscope: { url: http://pyroscope.test }\ntargets: [{ name: fpm, match: { comm: php-fpm }, max-processes: 1 }]\n",
			wantErr: "app is required",
		},
		{
			name:    "an empty phpspy path",
			content: minimal + "phpspy: ''\n",
			wantErr: "phpspy cannot be empty",
		},
		{
			name:    "no targets key",
			content: "pyroscope: { url: http://pyroscope.test }\napp: checkout\n",
			wantErr: "targets is required",
		},
		{
			name:    "an empty target list",
			content: "pyroscope: { url: http://pyroscope.test }\napp: checkout\ntargets: []\n",
			wantErr: "at least one target is required",
		},
		{
			name:    "a target without a name",
			content: "pyroscope: { url: http://pyroscope.test }\napp: checkout\ntargets: [{ match: { comm: php-fpm }, max-processes: 1 }]\n",
			wantErr: "targets[0]: name is required",
		},
		{
			name:    "a target name that cannot be a log field",
			content: "pyroscope: { url: http://pyroscope.test }\napp: checkout\ntargets: [{ name: 'fpm workers', match: { comm: php-fpm }, max-processes: 1 }]\n",
			wantErr: `name "fpm workers" may only contain`,
		},
		{
			name:    "two targets with one name",
			content: minimal + "  - name: fpm\n    match: { comm: php }\n    max-processes: 1\n",
			wantErr: `targets: name "fpm" is used twice`,
		},
		{
			name:    "a target without a match",
			content: "pyroscope: { url: http://pyroscope.test }\napp: checkout\ntargets: [{ name: fpm, max-processes: 1 }]\n",
			wantErr: `target "fpm": match is required`,
		},
		{
			name:    "an empty match",
			content: "pyroscope: { url: http://pyroscope.test }\napp: checkout\ntargets: [{ name: fpm, match: {}, max-processes: 1 }]\n",
			wantErr: "targets[0].match: at least one matcher is required",
		},
		{
			name:    "a match with only an exclusion",
			content: "pyroscope: { url: http://pyroscope.test }\napp: checkout\ntargets: [{ name: fpm, match: { exclude: master }, max-processes: 1 }]\n",
			wantErr: "exclude alone selects nothing",
		},
		{
			name:    "a cmdline regex that does not compile",
			content: "pyroscope: { url: http://pyroscope.test }\napp: checkout\ntargets: [{ name: fpm, match: { cmdline: '(pool' }, max-processes: 1 }]\n",
			wantErr: "targets[0].match.cmdline: invalid regex",
		},
		{
			name:    "an exclude regex that does not compile",
			content: "pyroscope: { url: http://pyroscope.test }\napp: checkout\ntargets: [{ name: fpm, match: { comm: php-fpm, exclude: '[' }, max-processes: 1 }]\n",
			wantErr: "targets[0].match.exclude: invalid regex",
		},
		{
			name:    "a uid that is not a number",
			content: "pyroscope: { url: http://pyroscope.test }\napp: checkout\ntargets: [{ name: fpm, match: { uid: www-data }, max-processes: 1 }]\n",
			wantErr: `targets[0].match.uid: expected an integer, got "www-data"`,
		},
		{
			name:    "a target without max-processes",
			content: "pyroscope: { url: http://pyroscope.test }\napp: checkout\ntargets: [{ name: fpm, match: { comm: php-fpm } }]\n",
			wantErr: `target "fpm": max-processes is required`,
		},
		{
			name:    "negative max-processes",
			content: "pyroscope: { url: http://pyroscope.test }\napp: checkout\ntargets: [{ name: fpm, match: { comm: php-fpm }, max-processes: -1 }]\n",
			wantErr: `target "fpm": max-processes is required and cannot be negative`,
		},
		{
			name:    "a zero rate",
			content: target("    rate: 0\n"),
			wantErr: `target "fpm": rate must be at least 1 Hz, got 0`,
		},
		{
			name:    "a scan interval below the minimum",
			content: target("    scan-interval: 50ms\n"),
			wantErr: `target "fpm": scan-interval must be at least 100ms, got 50ms`,
		},
		{
			name:    "a managed phpspy flag in short form",
			content: target("    phpspy-args: [-p, '1']\n"),
			wantErr: `target "fpm": phpspy-args: phpspy flag -p/--pid is managed by gospy and cannot be passed`,
		},
		{
			name:    "a managed phpspy flag in long form",
			content: target("    phpspy-args: [--rate-hz=99]\n"),
			wantErr: `target "fpm": phpspy-args: phpspy flag -H/--rate-hz is managed by gospy and cannot be passed`,
		},
		{
			name:    "a managed phpspy flag in a cluster",
			content: target("    phpspy-args: [-cT8]\n"),
			wantErr: `target "fpm": phpspy-args: phpspy flag -T/--threads is managed by gospy and cannot be passed`,
		},
		{
			name:    "a tag key with a bad character",
			content: target("    tags: { 'sour ce': fpm }\n"),
			wantErr: `target "fpm": tags: invalid tag key`,
		},
		{
			name:    "a static tag value with a comma",
			content: target("    tags: { source: 'fpm,web' }\n"),
			wantErr: `target "fpm": tags: invalid value of tag`,
		},
		{
			name:    "a dynamic tag that does not parse",
			content: target("    tags: { uri: '{{ \"glopeek server.REQUEST_URI\" }' }\n"),
			wantErr: `target "fpm": tags: invalid dynamic tag`,
		},
		{
			name:    "a global tag that does not parse",
			content: minimal + "tags: { env: 'prod uction' }\n",
			wantErr: `target "fpm": tags: invalid value of tag`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, _, err := config.Load([]byte(tc.content), env(nil))
			require.ErrorContains(t, err, tc.wantErr)
		})
	}
}

func TestLoadWarnsAboutTargetsThatShareStaticTags(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name         string
		targets      string
		wantWarnings []string
	}{
		{
			name: "distinct tag sets",
			targets: `
  - { name: fpm, match: { comm: php-fpm }, max-processes: 1, tags: { source: fpm } }
  - { name: cli, match: { comm: php }, max-processes: 1, tags: { source: cli } }
`,
		},
		{
			name: "identical tag sets",
			targets: `
  - { name: queue, match: { comm: php, cmdline: 'queue:listen' }, max-processes: 1, tags: { source: php } }
  - { name: cli, match: { comm: php }, max-processes: 1, tags: { source: php } }
`,
			wantWarnings: []string{`targets "queue" and "cli" carry the same static tags {env=production,source=php}: their samples merge into one series`},
		},
		{
			name: "no tags of their own",
			targets: `
  - { name: queue, match: { comm: php, cmdline: 'queue:listen' }, max-processes: 1 }
  - { name: cli, match: { comm: php }, max-processes: 1 }
`,
			wantWarnings: []string{`targets "queue" and "cli" carry the same static tags {env=production}: their samples merge into one series`},
		},
		{
			name: "dynamic tags do not tell them apart",
			targets: `
  - { name: queue, match: { comm: php, cmdline: 'queue:listen' }, max-processes: 1, tags: { uri: '{{ "uri" }}' } }
  - { name: cli, match: { comm: php }, max-processes: 1 }
`,
			wantWarnings: []string{`targets "queue" and "cli" carry the same static tags {env=production}: their samples merge into one series`},
		},
		{
			name: "a disabled target does not count",
			targets: `
  - { name: queue, match: { comm: php, cmdline: 'queue:listen' }, max-processes: 0 }
  - { name: cli, match: { comm: php }, max-processes: 1 }
`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			content := "pyroscope: { url: http://pyroscope.test }\napp: checkout\ntags: { env: production }\ntargets:" + tc.targets
			_, warnings := load(t, content, nil)
			require.Equal(t, tc.wantWarnings, warnings)
		})
	}
}

func TestLoadWarnsAboutATokenOverPlainHTTP(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name         string
		url          string
		token        string
		wantWarnings []string
	}{
		{
			name:  "https with a token",
			url:   "https://pyroscope.test",
			token: "secret",
		},
		{
			name: "http without a token",
			url:  "http://pyroscope.test",
		},
		{
			name:         "http with a token",
			url:          "http://pyroscope.test:4040",
			token:        "secret",
			wantWarnings: []string{"pyroscope.url http://pyroscope.test:4040 is plain http: the authentication token travels in cleartext"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			content := "pyroscope: { url: " + tc.url + " }\napp: checkout\ntargets: [{ name: fpm, match: { comm: php-fpm }, max-processes: 1 }]\n"
			env := map[string]string{}
			if tc.token != "" {
				env["GOSPY_PYROSCOPE_AUTH"] = tc.token
			}

			_, warnings := load(t, content, env)
			require.Equal(t, tc.wantWarnings, warnings)
		})
	}
}

func TestLoadKeepsTargetsInFileOrder(t *testing.T) {
	t.Parallel()

	cfg, _ := load(t, `
pyroscope: { url: http://pyroscope.test }
app: checkout
targets:
  - { name: queue, match: { comm: php, cmdline: 'queue:listen' }, max-processes: 3 }
  - { name: cron, match: { comm: php, cmdline: '/cronjobs/' }, max-processes: 2 }
  - { name: cli, match: { comm: php }, max-processes: 1 }
`, nil)

	names := make([]string, 0, len(cfg.Targets))
	for _, target := range cfg.Targets {
		names = append(names, target.Name)
	}
	require.Equal(t, []string{"queue", "cron", "cli"}, names, "a process belongs to the first target in file order")
}

func TestLoadAcceptsAnchorsAndAliases(t *testing.T) {
	t.Parallel()

	cfg, _ := load(t, `
pyroscope: { url: http://pyroscope.test }
app: checkout
targets:
  - { name: queue, match: { comm: php, cmdline: 'queue:listen' }, max-processes: 3, phpspy-args: &args [--php-version=74, -c] }
  - { name: cli, match: { comm: php }, max-processes: 1, phpspy-args: *args }
`, nil)

	require.Equal(t, []string{"--php-version=74", "-c"}, cfg.Targets[1].PhpspyArgs)
}

func TestMatcherMatches(t *testing.T) {
	t.Parallel()

	worker := procscan.Process{PID: 201, Comm: "php-fpm", Cmdline: "php-fpm: pool www", Exe: "/usr/local/sbin/php-fpm", UID: 33}
	master := procscan.Process{PID: 200, Comm: "php-fpm", Cmdline: "php-fpm: master process (/etc/php-fpm.conf)", Exe: "/usr/local/sbin/php-fpm", UID: 0}
	listener := procscan.Process{PID: 300, Comm: "php", Cmdline: "php console.php queue:listen", Exe: "/usr/local/bin/php", UID: 33}
	spy := procscan.Process{PID: 400, Comm: "phpspy", Cmdline: "phpspy -p 201 -H 25", Exe: "/usr/local/bin/phpspy", UID: 0}

	testCases := []struct {
		name    string
		matcher config.Matcher
		want    map[int]bool
	}{
		{
			name:    "comm is an exact match",
			matcher: config.Matcher{Comm: "php"},
			want:    map[int]bool{201: false, 200: false, 300: true, 400: false},
		},
		{
			name:    "cmdline is searched, not anchored",
			matcher: config.Matcher{Cmdline: regexp.MustCompile("queue:listen")},
			want:    map[int]bool{201: false, 200: false, 300: true, 400: false},
		},
		{
			name:    "matchers combine with and",
			matcher: config.Matcher{Comm: "php-fpm", Cmdline: regexp.MustCompile("^php-fpm: pool ")},
			want:    map[int]bool{201: true, 200: false, 300: false, 400: false},
		},
		{
			name:    "exclude rejects a match",
			matcher: config.Matcher{Comm: "php-fpm", Exclude: regexp.MustCompile("master process")},
			want:    map[int]bool{201: true, 200: false, 300: false, 400: false},
		},
		{
			name:    "exe is an exact path",
			matcher: config.Matcher{Exe: "/usr/local/sbin/php-fpm"},
			want:    map[int]bool{201: true, 200: true, 300: false, 400: false},
		},
		{
			name:    "uid",
			matcher: config.Matcher{UID: intPtr(33)},
			want:    map[int]bool{201: true, 200: false, 300: true, 400: false},
		},
		{
			name:    "a broad cmdline regex reaches phpspy itself",
			matcher: config.Matcher{Cmdline: regexp.MustCompile("php")},
			want:    map[int]bool{201: true, 200: true, 300: true, 400: true},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := map[int]bool{}
			for _, process := range []procscan.Process{worker, master, listener, spy} {
				got[process.PID] = tc.matcher.Matches(process)
			}
			require.Equal(t, tc.want, got)
		})
	}
}

func TestScanFieldsFollowTheEnabledMatchers(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		targets string
		want    procscan.Fields
	}{
		{
			name:    "comm and cmdline need nothing extra",
			targets: "[{ name: fpm, match: { comm: php-fpm, cmdline: pool }, max-processes: 1 }]",
			want:    0,
		},
		{
			name:    "uid",
			targets: "[{ name: fpm, match: { uid: 33 }, max-processes: 1 }]",
			want:    procscan.FieldUID,
		},
		{
			name:    "exe",
			targets: "[{ name: fpm, match: { exe: /usr/local/sbin/php-fpm }, max-processes: 1 }]",
			want:    procscan.FieldExe,
		},
		{
			name:    "a disabled target still claims its processes and needs its fields",
			targets: "[{ name: fpm, match: { exe: /usr/local/sbin/php-fpm, uid: 33 }, max-processes: 0 }]",
			want:    procscan.FieldUID | procscan.FieldExe,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg, _ := load(t, "pyroscope: { url: http://pyroscope.test }\napp: checkout\ntargets: "+tc.targets+"\n", nil)
			require.Equal(t, tc.want, cfg.ScanFields())
		})
	}
}

func TestLoadTreatsTagKeysAsTagsAndValuesAsWritten(t *testing.T) {
	t.Parallel()

	cfg, _ := load(t, `
pyroscope: { url: http://pyroscope.test }
app: checkout
targets:
  - name: fpm
    match: { comm: php-fpm }
    max-processes: 1
    tags:
      version: 1.0
      release: 2024-01-01
      debug: false
`, nil)

	require.Equal(t, "debug=false,release=2024-01-01,version=1.0", cfg.Targets[0].Parser.StaticTags, "a tag value is text however YAML would have typed it")
	require.Equal(t, map[string][]tag.DynamicTag{}, cfg.Targets[0].Parser.DynamicTags)
}

func TestLoadRejectsAnUnknownKeyNestedAnywhere(t *testing.T) {
	t.Parallel()

	content := strings.Replace(minimal, "max-processes: 5\n", "max-processes: 5\n    match-all: true\n", 1)
	_, _, err := config.Load([]byte(content), env(nil))
	require.ErrorContains(t, err, "targets[0].match-all: unknown key")
}
