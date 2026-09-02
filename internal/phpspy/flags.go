package phpspy

import (
	"strconv"
	"strings"
)

const (
	stdoutPath        = "-"
	foutHandler       = "fout"
	defaultBufferSize = 4096
	defaultRateHz     = 99

	// pipeBufSize is the PIPE_BUF above which phpspy interlaces writes in pgrep mode.
	pipeBufSize = 4096

	nanosecondsPerSecond = 1000000000
)

const (
	optionOutput           = "output"
	optionPgrep            = "pgrep"
	optionBufferSize       = "buffer-size"
	optionEventHandler     = "event-handler"
	optionEventHandlerOpts = "event-handler-opts"
	optionRateHz           = "rate-hz"
	optionSleepNs          = "sleep-ns"
)

type option struct {
	long  string
	short string
	value bool
}

// phpspy's getopt_long table. Reading one option needs the arity of them all: an option's
// value can look like a flag ("-f -v" filters on "-v") and switches cluster ("-cq").
var phpspyOptions = []option{
	{long: "help", short: "h"},
	{long: "pid", short: "p", value: true},
	{long: optionPgrep, short: "P", value: true},
	{long: "threads", short: "T", value: true},
	{long: optionSleepNs, short: "s", value: true},
	{long: optionRateHz, short: "H", value: true},
	{long: "php-version", short: "V", value: true},
	{long: "limit", short: "l", value: true},
	{long: "time-limit-ms", short: "i", value: true},
	{long: "max-depth", short: "n", value: true},
	{long: "request-info", short: "r", value: true},
	{long: "memory-usage", short: "m"},
	{long: optionOutput, short: "o", value: true},
	{long: "child-stdout", short: "O", value: true},
	{long: "child-stderr", short: "E", value: true},
	{long: "addr-executor-globals", short: "x", value: true},
	{long: "addr-sapi-globals", short: "a", value: true},
	{long: "single-line", short: "1"},
	{long: optionBufferSize, short: "b", value: true},
	{long: "filter", short: "f", value: true},
	{long: "filter-negate", short: "F", value: true},
	{long: "verbose-fields", short: "d", value: true},
	{long: "continue-on-error", short: "c"},
	{long: "quiet", short: "q"},
	{long: optionEventHandler, short: "j", value: true},
	{long: optionEventHandlerOpts, short: "J", value: true},
	{long: "comment", short: "#", value: true},
	{long: "nothing", short: "@"},
	{long: "version", short: "v"},
	{long: "pause-process", short: "S"},
	{long: "peek-var", short: "e", value: true},
	{long: "peek-global", short: "g", value: true},
	{long: "top", short: "t"},
	{long: "libname-awk-patt", short: "w", value: true},
}

func longOption(long string) (option, bool) {
	for _, opt := range phpspyOptions {
		if opt.long == long {
			return opt, true
		}
	}

	return option{}, false
}

func shortOption(short string) (option, bool) {
	for _, opt := range phpspyOptions {
		if opt.short == short {
			return opt, true
		}
	}

	return option{}, false
}

type givenOption struct {
	long  string
	value string
}

type givenOptions []givenOption

// parseArgs follows getopt_long: "--" ends the option list, a short option takes its value
// attached or from the next argument, and switches cluster into one word.
func parseArgs(args []string) givenOptions {
	given := make(givenOptions, 0, len(args))

	for i := 0; i < len(args); i++ {
		arg := args[i]

		switch {
		case arg == "--":
			return given
		case strings.HasPrefix(arg, "--"):
			name, inline, hasInline := strings.Cut(arg[2:], "=")
			opt, known := longOption(name)
			if !known {
				continue
			}

			switch {
			case !opt.value:
				given = append(given, givenOption{long: opt.long})
			case hasInline:
				given = append(given, givenOption{long: opt.long, value: inline})
			default:
				given = append(given, givenOption{long: opt.long, value: next(args, &i)})
			}
		case len(arg) > 1 && arg[0] == '-':
			given = appendCluster(given, arg, args, &i)
		}
	}

	return given
}

func appendCluster(given givenOptions, cluster string, args []string, i *int) givenOptions {
	for k := 1; k < len(cluster); k++ {
		opt, known := shortOption(cluster[k : k+1])
		if !known {
			return given
		}

		if !opt.value {
			given = append(given, givenOption{long: opt.long})
			continue
		}

		value := cluster[k+1:]
		if value == "" {
			value = next(args, i)
		}

		return append(given, givenOption{long: opt.long, value: value})
	}

	return given
}

func next(args []string, i *int) string {
	if *i+1 >= len(args) {
		return ""
	}

	*i++

	return args[*i]
}

func (given givenOptions) present(long string) bool {
	for _, opt := range given {
		if opt.long == long {
			return true
		}
	}

	return false
}

// text returns the last occurrence, as every phpspy option overwrites the previous one.
func (given givenOptions) text(long, defaultValue string) string {
	value := defaultValue
	for _, opt := range given {
		if opt.long == long {
			value = opt.value
		}
	}

	return value
}

func (given givenOptions) number(long string, defaultValue int) int {
	value, err := strconv.Atoi(given.text(long, ""))
	if err != nil {
		return defaultValue
	}

	return value
}
