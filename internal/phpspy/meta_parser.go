package phpspy

import (
	"github.com/rs/zerolog/log"
	"sort"
	"strings"
)

// parseMeta extracts and maps tags from metadata lines.
// It retains only the last occurrence of each mapped key, sorts the keys alphabetically,
// and logs a warning when duplicate keys are detected.
func parseMeta(lines []string, tagsMapping map[string]string) string {
	mappedTags := make(map[string]string)

	for _, line := range lines {
		if len(line) < 2 || !strings.HasPrefix(line, "# ") {
			continue
		}

		parts := strings.SplitN(line[2:], " = ", 2)
		if len(parts) != 2 {
			continue
		}

		originalKey, value := parts[0], strings.TrimSpace(parts[1])
		mappedKey, exists := tagsMapping[originalKey]
		if !exists {
			continue
		}

		// Check for duplicate keys and log a warning if a duplicate is found
		if oldValue, alreadyExists := mappedTags[mappedKey]; alreadyExists {
			log.Warn().
				Str("originalKey", originalKey).
				Str("mappedKey", mappedKey).
				Str("oldValue", oldValue).
				Str("newValue", value).
				Msg("Duplicate key detected, overwriting previous value")
		}

		// Overwrite with the latest value
		mappedTags[mappedKey] = value
	}

	// Collect and sort the keys alphabetically
	keys := make([]string, 0, len(mappedTags))
	for key := range mappedTags {
		keys = append(keys, key)
	}
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
