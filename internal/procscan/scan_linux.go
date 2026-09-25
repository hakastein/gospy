package procscan

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Field positions in /proc/<pid>/stat, counted from the field after the parenthesized comm:
// field 4 (ppid) and field 22 (starttime) in the numbering of proc(5).
const (
	statFieldPPID      = 4 - 3
	statFieldStartTime = 22 - 3
	statFieldCount     = statFieldStartTime + 1
)

const unreadUID = -1

// Scan reads every process directory under the root. A process that vanishes while it is
// read is skipped; anything that is not a process directory is ignored.
func (scanner *Scanner) Scan() ([]Process, error) {
	entries, err := os.ReadDir(scanner.root)
	if err != nil {
		return nil, fmt.Errorf("cannot read process table at %s: %w", scanner.root, err)
	}

	processes := make([]Process, 0, len(entries))
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 || !entry.IsDir() {
			continue
		}

		process, ok := scanner.read(pid)
		if !ok {
			continue
		}

		processes = append(processes, process)
	}

	return processes, nil
}

func (scanner *Scanner) read(pid int) (Process, bool) {
	dir := filepath.Join(scanner.root, strconv.Itoa(pid))

	process := Process{PID: pid, UID: unreadUID}
	if !readStat(filepath.Join(dir, "stat"), &process) {
		return Process{}, false
	}

	cmdline, err := os.ReadFile(filepath.Join(dir, "cmdline"))
	if err != nil {
		return Process{}, false
	}
	process.Cmdline = joinCmdline(cmdline)

	if scanner.fields&FieldUID != 0 {
		uid, ok := readUID(filepath.Join(dir, "status"))
		if !ok {
			return Process{}, false
		}
		process.UID = uid
	}

	if scanner.fields&FieldExe != 0 {
		// A kernel thread has no executable and another user's process hides it; both read as empty.
		process.Exe, _ = os.Readlink(filepath.Join(dir, "exe"))
	}

	return process, true
}

// readStat takes the comm from between the parentheses, as it may itself hold spaces and
// parentheses, and the numeric fields from after the last closing one.
func readStat(path string, process *Process) bool {
	content, err := os.ReadFile(path)
	if err != nil {
		return false
	}

	open := bytes.IndexByte(content, '(')
	closing := bytes.LastIndexByte(content, ')')
	if open < 0 || closing < open {
		return false
	}

	fields := strings.Fields(string(content[closing+1:]))
	if len(fields) < statFieldCount {
		return false
	}

	parent, err := strconv.Atoi(fields[statFieldPPID])
	if err != nil {
		return false
	}

	startTime, err := strconv.ParseUint(fields[statFieldStartTime], 10, 64)
	if err != nil {
		return false
	}

	process.Comm = string(content[open+1 : closing])
	process.ParentPID = parent
	process.StartTime = startTime

	return true
}

// joinCmdline turns the NUL-separated arguments into one line. A process that rewrote its
// title, as php-fpm does, pads the file with NULs, which are dropped rather than printed.
func joinCmdline(raw []byte) string {
	trimmed := bytes.TrimRight(raw, "\x00")

	return string(bytes.ReplaceAll(trimmed, []byte{0}, []byte{' '}))
}

// readUID returns the real user id, the first value of the Uid line in /proc/<pid>/status.
func readUID(path string) (int, bool) {
	content, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}

	for _, line := range strings.Split(string(content), "\n") {
		values, ok := strings.CutPrefix(line, "Uid:")
		if !ok {
			continue
		}

		fields := strings.Fields(values)
		if len(fields) == 0 {
			return 0, false
		}

		uid, err := strconv.Atoi(fields[0])
		if err != nil {
			return 0, false
		}

		return uid, true
	}

	return 0, false
}
