//go:build linux

package app

import "syscall"

// permanentStartErrnos are the exec errors no retry can fix: the file is not an
// executable, a path component is not a directory, or a shared library phpspy needs is
// corrupt. ELIBBAD exists only on Linux.
var permanentStartErrnos = []error{syscall.ENOEXEC, syscall.ENOTDIR, syscall.ELIBBAD}
