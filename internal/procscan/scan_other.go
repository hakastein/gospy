//go:build !linux

package procscan

import "errors"

// ErrUnsupported is what Scan returns where there is no procfs to read.
var ErrUnsupported = errors.New("process discovery needs procfs and is only implemented on linux")

// Scan reports ErrUnsupported: the darwin build of gospy compiles and reports its version,
// but only Linux runs phpspy.
func (scanner *Scanner) Scan() ([]Process, error) {
	return nil, ErrUnsupported
}
