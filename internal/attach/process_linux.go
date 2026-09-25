package attach

import "syscall"

// Own process group so that a detach also reaches the helpers phpspy shells out to while it
// attaches, and the parent-death signal so that a phpspy does not outlive a killed gospy. The
// kernel clears that signal on the exec of a binary that raises privileges, so a phpspy
// carrying file capabilities under an unprivileged gospy only gets the process group.
func processAttributes() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGTERM}
}
