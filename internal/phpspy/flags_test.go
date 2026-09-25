package phpspy_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/hakastein/gospy/internal/phpspy"
)

func TestValidateArgs(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name: "no arguments",
			args: nil,
		},
		{
			name: "tuning flags pass through",
			args: []string{"--max-depth=-1", "--php-version=74", "--peek-global=server.REQUEST_URI", "-c", "-b", "65536"},
		},
		{
			name: "a value that looks like a switch is not read as one",
			args: []string{"-f", "-v"},
		},
		{
			name: "an abbreviated flag that is not managed",
			args: []string{"--php=74", "--peek-g", "server.REQUEST_URI", "--cont"},
		},
		{
			name: "flags of phpspy's unreleased master",
			args: []string{"-N", "5", "--max-depth-outer=3", "-D", "--peek-pdo", "-cD"},
		},
		{
			name: "a flag phpspy does not know is left to phpspy",
			args: []string{"--future-flag", "--other=value", "-Z"},
		},
		{
			name: "a letter phpspy does not know inside a cluster",
			args: []string{"-Zc"},
		},
		{
			name:    "pid in short form",
			args:    []string{"-p", "123"},
			wantErr: "phpspy flag -p/--pid is managed by gospy and cannot be passed",
		},
		{
			name:    "pid in long form",
			args:    []string{"--pid=123"},
			wantErr: "phpspy flag -p/--pid is managed by gospy and cannot be passed",
		},
		{
			name:    "pid attached to its short flag",
			args:    []string{"-p123"},
			wantErr: "phpspy flag -p/--pid is managed by gospy and cannot be passed",
		},
		{
			name:    "pid abbreviated",
			args:    []string{"--pi=1"},
			wantErr: "phpspy flag -p/--pid is managed by gospy and cannot be passed",
		},
		{
			name:    "the rate abbreviated",
			args:    []string{"--rate=250"},
			wantErr: "phpspy flag -H/--rate-hz is managed by gospy and cannot be passed",
		},
		{
			name:    "the rate abbreviated with a separate value",
			args:    []string{"--rate", "250"},
			wantErr: "phpspy flag -H/--rate-hz is managed by gospy and cannot be passed",
		},
		{
			name:    "top abbreviated",
			args:    []string{"--to"},
			wantErr: "phpspy flag -t/--top is managed by gospy and cannot be passed",
		},
		{
			name:    "the sleep interval abbreviated",
			args:    []string{"--sl=1000000"},
			wantErr: "phpspy flag -s/--sleep-ns is managed by gospy and cannot be passed",
		},
		{
			name:    "an ambiguous abbreviation",
			args:    []string{"--p", "1"},
			wantErr: "phpspy flag --p is ambiguous: it could be --pid, --pgrep, --php-version, --pause-process, --peek-var, --peek-global, --peek-pdo",
		},
		{
			name:    "an abbreviation that master made ambiguous",
			args:    []string{"--max=3"},
			wantErr: "phpspy flag --max is ambiguous: it could be --max-depth, --max-depth-outer",
		},
		{
			name:    "a managed flag inside a cluster",
			args:    []string{"-cT", "8"},
			wantErr: "phpspy flag -T/--threads is managed by gospy and cannot be passed",
		},
		{
			name:    "a managed switch inside a cluster",
			args:    []string{"-c1"},
			wantErr: "phpspy flag -1/--single-line is managed by gospy and cannot be passed",
		},
		{
			name:    "a managed flag behind a letter phpspy does not know",
			args:    []string{"-Zt"},
			wantErr: "phpspy flag -t/--top is managed by gospy and cannot be passed",
		},
		{
			name:    "a managed flag with a value behind a letter phpspy does not know",
			args:    []string{"-ZH", "1"},
			wantErr: "phpspy flag -H/--rate-hz is managed by gospy and cannot be passed",
		},
		{
			name:    "pgrep mode",
			args:    []string{"-P", "php-fpm"},
			wantErr: "phpspy flag -P/--pgrep is managed by gospy and cannot be passed",
		},
		{
			name:    "the rate",
			args:    []string{"-H", "99"},
			wantErr: "phpspy flag -H/--rate-hz is managed by gospy and cannot be passed",
		},
		{
			name:    "the sleep interval",
			args:    []string{"--sleep-ns", "10000000"},
			wantErr: "phpspy flag -s/--sleep-ns is managed by gospy and cannot be passed",
		},
		{
			name:    "the time limit",
			args:    []string{"-i", "59000"},
			wantErr: "phpspy flag -i/--time-limit-ms is managed by gospy and cannot be passed",
		},
		{
			name:    "the sample limit",
			args:    []string{"--limit=100"},
			wantErr: "phpspy flag -l/--limit is managed by gospy and cannot be passed",
		},
		{
			name:    "an output path",
			args:    []string{"-o", "-"},
			wantErr: "phpspy flag -o/--output is managed by gospy and cannot be passed",
		},
		{
			name:    "an event handler",
			args:    []string{"--event-handler=fout"},
			wantErr: "phpspy flag -j/--event-handler is managed by gospy and cannot be passed",
		},
		{
			name:    "top mode",
			args:    []string{"-t"},
			wantErr: "phpspy flag -t/--top is managed by gospy and cannot be passed",
		},
		{
			name:    "quiet silences the errors gospy reads",
			args:    []string{"-q"},
			wantErr: "phpspy flag -q/--quiet is managed by gospy and cannot be passed",
		},
		{
			name:    "version",
			args:    []string{"--version"},
			wantErr: "phpspy flag -v/--version is managed by gospy and cannot be passed",
		},
		{
			name:    "help",
			args:    []string{"-h"},
			wantErr: "phpspy flag -h/--help is managed by gospy and cannot be passed",
		},
		{
			name:    "a switch written with a value is still a switch",
			args:    []string{"--top=false"},
			wantErr: "phpspy flag -t/--top is managed by gospy and cannot be passed",
		},
		{
			name:    "a bare word ends phpspy's option parsing",
			args:    []string{"php", "-c"},
			wantErr: `phpspy argument "php" is not a flag: phpspy stops reading options at the first bare word`,
		},
		{
			name:    "a command to trace",
			args:    []string{"-c", "--", "php", "script.php"},
			wantErr: `phpspy argument "--" is not a flag`,
		},
		{
			name:    "a value for a flag phpspy does not know has to be inline",
			args:    []string{"--future-flag", "value"},
			wantErr: `phpspy argument "value" is not a flag`,
		},
		{
			name:    "a flag without its value",
			args:    []string{"-c", "--max-depth"},
			wantErr: "phpspy flag -n/--max-depth needs a value",
		},
		{
			name:    "a short flag without its value",
			args:    []string{"-cb"},
			wantErr: "phpspy flag -b/--buffer-size needs a value",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := phpspy.ValidateArgs(tc.args)

			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}

			require.ErrorContains(t, err, tc.wantErr)
		})
	}
}

func TestWithDefaults(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "no arguments",
			args: nil,
			want: []string{"--buffer-size=1048576"},
		},
		{
			name: "arguments without a buffer size",
			args: []string{"--max-depth=-1", "-c"},
			want: []string{"--max-depth=-1", "-c", "--buffer-size=1048576"},
		},
		{
			name: "a short buffer size with its value apart",
			args: []string{"-b", "65536"},
			want: []string{"-b", "65536"},
		},
		{
			name: "a short buffer size with its value attached",
			args: []string{"-b65536"},
			want: []string{"-b65536"},
		},
		{
			name: "a buffer size clustered after a switch",
			args: []string{"-cb", "65536"},
			want: []string{"-cb", "65536"},
		},
		{
			name: "a long buffer size",
			args: []string{"--buffer-size=65536"},
			want: []string{"--buffer-size=65536"},
		},
		{
			name: "an abbreviated long buffer size",
			args: []string{"--buffer", "65536"},
			want: []string{"--buffer", "65536"},
		},
		{
			name: "a -b that is the value of another flag",
			args: []string{"-f", "-b"},
			want: []string{"-f", "-b", "--buffer-size=1048576"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.want, phpspy.WithDefaults(tc.args))
		})
	}
}
