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
			name: "a flag phpspy does not know is left to phpspy",
			args: []string{"--future-flag", "-Z"},
		},
		{
			name: "arguments of a traced command are not phpspy flags",
			args: []string{"-c", "--", "php", "-p", "1"},
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
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := phpspy.ValidateArgs(tc.args)

			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}

			require.EqualError(t, err, tc.wantErr)
		})
	}
}
