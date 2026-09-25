// Package procscan reads the process table out of a procfs tree.
package procscan

// DefaultRoot is where the kernel mounts procfs.
const DefaultRoot = "/proc"

// Process is one entry of the process table as a Scan saw it. PID and StartTime together
// identify a process for its whole life: a reused PID has a later start time.
type Process struct {
	PID       int
	ParentPID int
	// StartTime is the process start in clock ticks since boot, field 22 of /proc/<pid>/stat.
	StartTime uint64
	// UID is the real user id; -1 when it was not read.
	UID int
	// Comm is the executable name as /proc/<pid>/comm reports it, at most 15 characters.
	Comm string
	// Cmdline is the command line with its NUL separators replaced by spaces.
	Cmdline string
	// Exe is the executable path, without the " (deleted)" mark of a binary replaced on disk;
	// empty when the link could not be read.
	Exe string
}

// Fields selects the optional files a Scan reads for every process. Start time, parent,
// comm and command line are always read; the user id and the executable path cost an extra
// file each per process per scan.
type Fields uint8

const (
	FieldUID Fields = 1 << iota
	FieldExe
)

// Scanner reads a procfs tree. It is not safe for concurrent use.
type Scanner struct {
	root   string
	fields Fields
	buffer []byte
}

// New returns a Scanner over the procfs tree at root; an empty root means DefaultRoot.
func New(root string, fields Fields) *Scanner {
	if root == "" {
		root = DefaultRoot
	}

	return &Scanner{root: root, fields: fields}
}
