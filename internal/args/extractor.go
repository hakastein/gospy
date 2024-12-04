package args

import (
	"strconv"
	"strings"
)

func ExtractFlagValue[T any](flags []string, longKey, shortKey string, defaultValue T) T {
	shortKey, longKey = "-"+shortKey, "--"+longKey
	flagLen := len(flags)

	for i := 0; i < flagLen; i++ {
		flag := flags[i]
		switch {
		case strings.HasPrefix(flag, longKey+"="):
			return convertTo[T](strings.TrimPrefix(flag, longKey+"="))
		case flag == shortKey && i+1 < flagLen:
			return convertTo[T](flags[i+1])
		case flag == longKey || flag == shortKey:
			if _, ok := any(defaultValue).(bool); ok {
				return convertTo[T]("true")
			}
		}
	}
	return defaultValue
}

func convertTo[T any](value string) T {
	var result T
	switch any(result).(type) {
	case string:
		return any(value).(T)
	case int:
		if intValue, err := strconv.Atoi(value); err == nil {
			return any(intValue).(T)
		}
	case bool:
		if boolValue, err := strconv.ParseBool(value); err == nil {
			return any(boolValue).(T)
		}
	}
	return result
}
