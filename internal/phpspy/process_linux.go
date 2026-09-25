package phpspy

import "syscall"

// Own process group so cancellation also reaches the children phpspy forks in pgrep mode.
func sessionAttributes() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGTERM}
}
