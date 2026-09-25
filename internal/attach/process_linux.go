package attach

import "syscall"

// Own process group so that a detach also reaches the helpers phpspy shells out to while it
// attaches, and the parent-death signal so that no phpspy outlives a killed gospy.
func sessionAttributes() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGTERM}
}
