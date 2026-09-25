//go:build unix && !linux

package attach

import "syscall"

// Only Linux has a parent-death signal; elsewhere phpspy just leads its own process group.
func sessionAttributes() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
