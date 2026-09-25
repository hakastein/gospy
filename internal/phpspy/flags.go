package phpspy

import (
	"fmt"
	"strings"
)

type option struct {
	long  string
	short string
	value bool
	// managed marks an option gospy sets itself, or one that would make phpspy trace something
	// else or print something other than trace blocks; it cannot come from the configuration.
	managed bool
}

// phpspy's getopt_long table. Reading one option needs the arity of them all: an option's
// value can look like a flag ("-f -v" filters on "-v") and switches cluster ("-cq").
var phpspyOptions = []option{
	{long: "help", short: "h", managed: true},
	{long: "pid", short: "p", value: true, managed: true},
	{long: "pgrep", short: "P", value: true, managed: true},
	{long: "threads", short: "T", value: true, managed: true},
	{long: "sleep-ns", short: "s", value: true, managed: true},
	{long: "rate-hz", short: "H", value: true, managed: true},
	{long: "php-version", short: "V", value: true},
	{long: "limit", short: "l", value: true, managed: true},
	{long: "time-limit-ms", short: "i", value: true, managed: true},
	{long: "max-depth", short: "n", value: true},
	{long: "request-info", short: "r", value: true},
	{long: "memory-usage", short: "m"},
	{long: "output", short: "o", value: true, managed: true},
	{long: "child-stdout", short: "O", value: true},
	{long: "child-stderr", short: "E", value: true},
	{long: "addr-executor-globals", short: "x", value: true},
	{long: "addr-sapi-globals", short: "a", value: true},
	{long: "single-line", short: "1", managed: true},
	{long: "buffer-size", short: "b", value: true},
	{long: "filter", short: "f", value: true},
	{long: "filter-negate", short: "F", value: true},
	{long: "verbose-fields", short: "d", value: true},
	{long: "continue-on-error", short: "c"},
	// quiet silences the copy_proc_mem lines the silent-denial watchdog reads.
	{long: "quiet", short: "q", managed: true},
	{long: "event-handler", short: "j", value: true, managed: true},
	{long: "event-handler-opts", short: "J", value: true},
	{long: "comment", short: "#", value: true},
	{long: "nothing", short: "@"},
	{long: "version", short: "v", managed: true},
	{long: "pause-process", short: "S"},
	{long: "peek-var", short: "e", value: true},
	{long: "peek-global", short: "g", value: true},
	{long: "top", short: "t", managed: true},
	{long: "libname-awk-patt", short: "w", value: true},
}

// ValidateArgs rejects extra phpspy arguments that phpspy would not read as gospy means them:
// an option gospy manages, in short, long, abbreviated or clustered form; an option without
// its value; an ambiguous abbreviation; and a bare word, at which phpspy stops reading
// options altogether. Options phpspy 0.7.0 does not know are passed through untouched.
func ValidateArgs(args []string) error {
	given, err := parseArgs(args)
	if err != nil {
		return err
	}

	for _, opt := range given {
		if opt.managed {
			return fmt.Errorf("phpspy flag -%s/--%s is managed by gospy and cannot be passed", opt.short, opt.long)
		}
	}

	return nil
}

// longOption resolves a long option the way getopt_long does: an exact name wins, otherwise
// the one option the text is a prefix of. An unknown name resolves to nothing.
func longOption(name string) (*option, error) {
	var matches []*option
	for index := range phpspyOptions {
		opt := &phpspyOptions[index]
		if opt.long == name {
			return opt, nil
		}
		if strings.HasPrefix(opt.long, name) {
			matches = append(matches, opt)
		}
	}

	switch len(matches) {
	case 0:
		return nil, nil
	case 1:
		return matches[0], nil
	default:
		names := make([]string, 0, len(matches))
		for _, opt := range matches {
			names = append(names, "--"+opt.long)
		}

		return nil, fmt.Errorf("phpspy flag --%s is ambiguous: it could be %s", name, strings.Join(names, ", "))
	}
}

func shortOption(short byte) (*option, bool) {
	for index := range phpspyOptions {
		if phpspyOptions[index].short == string(short) {
			return &phpspyOptions[index], true
		}
	}

	return nil, false
}

// parseArgs follows getopt_long as phpspy calls it: a long option may be abbreviated, a short
// option takes its value attached or from the next argument, switches cluster into one word,
// and a letter phpspy does not know is skipped. phpspy stops at the first word that is not an
// option and never sees the flags after it, so a bare word is an error here; a value for an
// option the table does not know has to be written inline (--flag=value).
func parseArgs(args []string) ([]option, error) {
	var given []option

	for i := 0; i < len(args); i++ {
		arg := args[i]

		switch {
		case arg == "--" || arg == "-" || !strings.HasPrefix(arg, "-"):
			return nil, fmt.Errorf("phpspy argument %q is not a flag: phpspy stops reading options at the first bare word", arg)
		case strings.HasPrefix(arg, "--"):
			name, _, hasInline := strings.Cut(arg[2:], "=")
			opt, err := longOption(name)
			if err != nil {
				return nil, err
			}
			if opt == nil {
				continue
			}

			given = append(given, *opt)
			if opt.value && !hasInline {
				if i, err = valueAt(args, i, opt); err != nil {
					return nil, err
				}
			}
		default:
			var err error
			if given, i, err = appendCluster(given, arg, args, i); err != nil {
				return nil, err
			}
		}
	}

	return given, nil
}

func appendCluster(given []option, cluster string, args []string, i int) ([]option, int, error) {
	for k := 1; k < len(cluster); k++ {
		opt, known := shortOption(cluster[k])
		if !known {
			continue
		}

		given = append(given, *opt)
		if !opt.value {
			continue
		}

		if cluster[k+1:] != "" {
			return given, i, nil
		}

		i, err := valueAt(args, i, opt)

		return given, i, err
	}

	return given, i, nil
}

// valueAt consumes the argument after i as the value of opt.
func valueAt(args []string, i int, opt *option) (int, error) {
	if i+1 >= len(args) {
		return i, fmt.Errorf("phpspy flag -%s/--%s needs a value", opt.short, opt.long)
	}

	return i + 1, nil
}
