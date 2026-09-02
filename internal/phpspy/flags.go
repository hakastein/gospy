package phpspy

import (
	"strconv"
	"strings"
)

type flag struct {
	long  string
	short string
}

// A bare "-s" or "--long" turns a phpspy switch on; "--long=false" turns it off.
func (f flag) enabled(args []string) bool {
	long, short := "--"+f.long, "-"+f.short

	for _, arg := range args {
		switch {
		case arg == long || arg == short:
			return true
		case strings.HasPrefix(arg, long+"="):
			on, err := strconv.ParseBool(strings.TrimPrefix(arg, long+"="))
			return err == nil && on
		}
	}

	return false
}

// phpspy options carry their argument as "--long=text", "--long text" or "-s text".
func (f flag) text(args []string, defaultValue string) string {
	long, short := "--"+f.long, "-"+f.short

	for i, arg := range args {
		switch {
		case strings.HasPrefix(arg, long+"="):
			return strings.TrimPrefix(arg, long+"=")
		case (arg == long || arg == short) && i+1 < len(args):
			return args[i+1]
		}
	}

	return defaultValue
}

func (f flag) number(args []string, defaultValue int) int {
	value, err := strconv.Atoi(f.text(args, ""))
	if err != nil {
		return defaultValue
	}

	return value
}
