//go:build !linux

package app

import "syscall"

// permanentStartErrnos are the exec errors no retry can fix: the file is not an
// executable or a path component is not a directory.
var permanentStartErrnos = []error{syscall.ENOEXEC, syscall.ENOTDIR}
