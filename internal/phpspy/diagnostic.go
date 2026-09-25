package phpspy

import (
	"slices"
	"strings"
)

// copy_proc_mem's names for the reads of phpspy 0.7.0's stack walk. Under --continue-on-error
// a failed one still emits the frames read so far, without the meta lines.
var stackReads = []string{"execute_data", "zfunc", "zce", "function_name", "class_name", "filename"}

func isFrame(line string) bool {
	digits := 0
	for digits < len(line) && line[digits] >= '0' && line[digits] <= '9' {
		digits++
	}

	return digits > 0 && digits < len(line) && line[digits] == ' '
}

// cutsStack reports a diagnostic after which phpspy emits whatever part of the trace it had:
// a full buffer, a failed read inside the stack walk, or the traced process gone mid-walk.
func cutsStack(line string) bool {
	switch {
	case strings.HasPrefix(line, "event_handler_fout_snprintf:"), strings.HasPrefix(line, "process_vm_readv:"):
		return true
	case strings.HasPrefix(line, "copy_proc_mem:"):
		return slices.Contains(stackReads, copiedField(line))
	default:
		return false
	}
}

func copiedField(line string) string {
	for _, prefix := range []string{"copy_proc_mem: Failed to copy ", "copy_proc_mem: Not copying "} {
		if rest, found := strings.CutPrefix(line, prefix); found {
			what, _, _ := strings.Cut(rest, ";")
			return what
		}
	}

	return ""
}
