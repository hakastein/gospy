//go:build unix && !linux

package phpspy

import "syscall"

// Only Linux has a parent-death signal; elsewhere the profiler just leads its own process group.
func sessionAttributes() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
