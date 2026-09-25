package procscan

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// Field positions in /proc/<pid>/stat, counted from the field after the parenthesized comm:
// field 4 (ppid) and field 22 (starttime) in the numbering of proc(5).
const (
	statFieldPPID      = 4 - 3
	statFieldStartTime = 22 - 3
)

const (
	unreadUID = -1
	// initialBuffer holds any stat or status file and most command lines in one read.
	initialBuffer = 8 << 10
	// A binary replaced on disk keeps running, and its exe link says so.
	deletedSuffix = " (deleted)"
)

// Scan reads every process directory under the root. A process that vanishes while it is
// read is skipped; anything that is not a process directory is ignored. A Scanner reads
// through one reused buffer, so it is not safe for concurrent use.
func (scanner *Scanner) Scan() ([]Process, error) {
	dir, err := os.Open(scanner.root)
	if err != nil {
		return nil, fmt.Errorf("cannot read process table at %s: %w", scanner.root, err)
	}
	names, err := dir.Readdirnames(-1)
	_ = dir.Close()
	if err != nil {
		return nil, fmt.Errorf("cannot read process table at %s: %w", scanner.root, err)
	}

	processes := make([]Process, 0, len(names))
	for _, name := range names {
		pid, err := strconv.Atoi(name)
		if err != nil || pid <= 0 {
			continue
		}

		process, ok := scanner.read(scanner.root+"/"+name, pid)
		if !ok {
			continue
		}

		processes = append(processes, process)
	}

	return processes, nil
}

func (scanner *Scanner) read(dir string, pid int) (Process, bool) {
	process := Process{PID: pid, UID: unreadUID}

	content, err := scanner.readFile(dir + "/stat")
	if err != nil || !parseStat(content, &process) {
		return Process{}, false
	}

	content, err = scanner.readFile(dir + "/cmdline")
	if err != nil {
		return Process{}, false
	}
	process.Cmdline = joinCmdline(content)

	if scanner.fields&FieldUID != 0 {
		content, err = scanner.readFile(dir + "/status")
		if err != nil {
			return Process{}, false
		}

		uid, ok := parseUID(content)
		if !ok {
			return Process{}, false
		}
		process.UID = uid
	}

	if scanner.fields&FieldExe != 0 {
		// A kernel thread has no executable and another user's process hides it; both read as empty.
		exe, _ := os.Readlink(dir + "/exe")
		process.Exe = strings.TrimSuffix(exe, deletedSuffix)
	}

	return process, true
}

// readFile reads a procfs file into the scanner's buffer with plain system calls: os.ReadFile
// would register every file with the poller and grow its buffer from scratch, several times
// the cost on a table of a thousand processes.
func (scanner *Scanner) readFile(path string) ([]byte, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	defer syscall.Close(fd)

	if scanner.buffer == nil {
		scanner.buffer = make([]byte, initialBuffer)
	}

	filled := 0
	for {
		if filled == len(scanner.buffer) {
			scanner.buffer = append(scanner.buffer, make([]byte, len(scanner.buffer))...)
		}

		read, err := syscall.Read(fd, scanner.buffer[filled:])
		if err == syscall.EINTR {
			continue
		}
		if err != nil {
			return nil, err
		}
		if read == 0 {
			return scanner.buffer[:filled], nil
		}

		filled += read
	}
}

// parseStat takes the comm from between the parentheses, as it may itself hold spaces and
// parentheses, and the numeric fields from after the last closing one.
func parseStat(content []byte, process *Process) bool {
	open := bytes.IndexByte(content, '(')
	closing := bytes.LastIndexByte(content, ')')
	if open < 0 || closing < open {
		return false
	}

	parent, ok := field(content[closing+1:], statFieldPPID)
	if !ok {
		return false
	}

	start, ok := field(content[closing+1:], statFieldStartTime)
	if !ok {
		return false
	}

	ppid, err := strconv.Atoi(string(parent))
	if err != nil {
		return false
	}

	startTime, err := strconv.ParseUint(string(start), 10, 64)
	if err != nil {
		return false
	}

	process.Comm = string(content[open+1 : closing])
	process.ParentPID = ppid
	process.StartTime = startTime

	return true
}

// field returns the index-th whitespace-separated field of content, counted from zero.
func field(content []byte, index int) ([]byte, bool) {
	for {
		content = bytes.TrimLeft(content, " \t\n")
		if len(content) == 0 {
			return nil, false
		}

		end := bytes.IndexAny(content, " \t\n")
		if end < 0 {
			end = len(content)
		}

		if index == 0 {
			return content[:end], true
		}

		index--
		content = content[end:]
	}
}

// joinCmdline turns the NUL-separated arguments into one line, in place. A process that
// rewrote its title, as php-fpm does, pads the file with NULs, which are dropped rather than printed.
func joinCmdline(raw []byte) string {
	trimmed := bytes.TrimRight(raw, "\x00")
	for i, b := range trimmed {
		if b == 0 {
			trimmed[i] = ' '
		}
	}

	return string(trimmed)
}

// parseUID returns the real user id, the first value of the Uid line in /proc/<pid>/status.
func parseUID(content []byte) (int, bool) {
	const label = "\nUid:"

	start := bytes.Index(content, []byte(label))
	if start < 0 {
		return 0, false
	}

	value, ok := field(content[start+len(label):], 0)
	if !ok {
		return 0, false
	}

	uid, err := strconv.Atoi(string(value))
	if err != nil {
		return 0, false
	}

	return uid, true
}
