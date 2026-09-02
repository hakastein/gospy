package phpspy

import (
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/hakastein/gospy/internal/tag"
)

// Keys are sorted so an identical tag set always yields the same string: the collector
// groups Batches by that string.
func metaToTags(lines []string, tagsMapping map[string][]tag.DynamicTag) string {
	if len(tagsMapping) == 0 || len(lines) == 0 {
		return ""
	}

	mappedTags := make(map[string]string)

	for _, line := range lines {
		if len(line) < 2 || !strings.HasPrefix(line, "# ") {
			continue
		}

		parts := strings.SplitN(line[2:], " = ", 2)
		if len(parts) != 2 {
			continue
		}

		originalKey, originalValue := parts[0], strings.TrimSpace(parts[1])
		dynamicTags, exists := tagsMapping[originalKey]
		if !exists {
			continue
		}

		for _, dynamicTag := range dynamicTags {
			mappedValue := dynamicTag.GetValue(originalValue)

			if oldValue, alreadyExists := mappedTags[dynamicTag.TagKey]; alreadyExists {
				log.Warn().
					Str("originalKey", originalKey).
					Str("mappedKey", dynamicTag.TagKey).
					Str("oldValue", oldValue).
					Str("newValue", mappedValue).
					Msg("Duplicate key detected, overwriting previous value")
			}

			mappedTags[dynamicTag.TagKey] = mappedValue
		}
	}

	if len(mappedTags) == 0 {
		return ""
	}

	keys := slices.Collect(maps.Keys(mappedTags))
	sort.Strings(keys)

	var tags strings.Builder
	for i, key := range keys {
		if i > 0 {
			tags.WriteRune(',')
		}
		tags.WriteString(key)
		tags.WriteRune('=')
		tags.WriteString(mappedTags[key])
	}

	return tags.String()
}
