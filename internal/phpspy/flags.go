package phpspy

import (
	"fmt"
	"strings"
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
	{long: "pgrep", short: "P", value: true},
	{long: "threads", short: "T", value: true},
	{long: "sleep-ns", short: "s", value: true},
	{long: "rate-hz", short: "H", value: true},
	{long: "php-version", short: "V", value: true},
	{long: "limit", short: "l", value: true},
	{long: "time-limit-ms", short: "i", value: true},
	{long: "max-depth", short: "n", value: true},
	{long: "request-info", short: "r", value: true},
	{long: "memory-usage", short: "m"},
	{long: "output", short: "o", value: true},
	{long: "child-stdout", short: "O", value: true},
	{long: "child-stderr", short: "E", value: true},
	{long: "addr-executor-globals", short: "x", value: true},
	{long: "addr-sapi-globals", short: "a", value: true},
	{long: "single-line", short: "1"},
	{long: "buffer-size", short: "b", value: true},
	{long: "filter", short: "f", value: true},
	{long: "filter-negate", short: "F", value: true},
	{long: "verbose-fields", short: "d", value: true},
	{long: "continue-on-error", short: "c"},
	{long: "quiet", short: "q"},
	{long: "event-handler", short: "j", value: true},
	{long: "event-handler-opts", short: "J", value: true},
	{long: "comment", short: "#", value: true},
	{long: "nothing", short: "@"},
	{long: "version", short: "v"},
	{long: "pause-process", short: "S"},
	{long: "peek-var", short: "e", value: true},
	{long: "peek-global", short: "g", value: true},
	{long: "top", short: "t"},
	{long: "libname-awk-patt", short: "w", value: true},
}

// Options gospy sets itself, or that would make phpspy trace something else or print
// something other than trace blocks. The rate and the target set have exactly one source of
// truth: the configuration file.
var managedOptions = []string{
	"pid",
	"pgrep",
	"threads",
	"rate-hz",
	"sleep-ns",
	"time-limit-ms",
	"limit",
	"output",
	"event-handler",
	"top",
	"single-line",
	"version",
	"help",
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

// ValidateArgs rejects extra phpspy arguments that name an option gospy manages, in short,
// long or clustered form. Options phpspy does not know are passed through untouched.
func ValidateArgs(args []string) error {
	given := parseArgs(args)

	for _, managed := range managedOptions {
		if !given.present(managed) {
			continue
		}

		opt, _ := longOption(managed)

		return fmt.Errorf("phpspy flag -%s/--%s is managed by gospy and cannot be passed", opt.short, opt.long)
	}

	return nil
}

type givenOptions []string

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
			name, _, hasInline := strings.Cut(arg[2:], "=")
			opt, known := longOption(name)
			if !known {
				continue
			}

			given = append(given, opt.long)
			if opt.value && !hasInline {
				skip(args, &i)
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

		given = append(given, opt.long)
		if !opt.value {
			continue
		}

		if cluster[k+1:] == "" {
			skip(args, i)
		}

		return given
	}

	return given
}

// skip consumes the next argument as the value of the option just read.
func skip(args []string, i *int) {
	if *i+1 < len(args) {
		*i++
	}
}

func (given givenOptions) present(long string) bool {
	for _, opt := range given {
		if opt == long {
			return true
		}
	}

	return false
}
