package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/rs/zerolog/log"
	"runtime/debug"
	"strings"
)

// parseTags processes input tags and separates static and dynamic tags
func parseTags(tagsInput []string) (string, map[string]string, error) {
	dynamicTags := make(map[string]string)
	var staticTags []string

	for _, tag := range tagsInput {
		idx := strings.Index(tag, "=")
		if idx != -1 {
			key := tag[:idx]
			value := tag[idx+1:]
			if len(value) > 1 && value[0] == '%' && value[len(value)-1] == '%' {
				dynamicTags[value[1:len(value)-1]] = key
			} else {
				staticTags = append(staticTags, tag)
			}
		} else {
			return "", nil, fmt.Errorf("unexpected tag format %s, expected tag=value", tag)
		}
	}

	return strings.Join(staticTags, ","), dynamicTags, nil
}

func mapEntryPoints(entryPoints []string) map[string]struct{} {
	entryMap := make(map[string]struct{}, len(entryPoints))
	for _, entry := range entryPoints {
		entryMap[entry] = struct{}{}
	}
	return entryMap
}

func recoverAndCancel(message string, cancel context.CancelFunc) {
	if r := recover(); r != nil {
		var err error
		switch x := r.(type) {
		case string:
			err = errors.New(x)
		case error:
			err = x
		default:
			err = errors.New("unknown panic")
		}
		log.Err(err).
			Stack().
			Str("stack", string(debug.Stack())).
			Msg(message)
		cancel()
	}
}
