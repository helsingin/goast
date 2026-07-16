//go:build cgo

package worker

import "C"

// CgoCall must be absent from typed references when cgo is disabled.
func CgoCall(r Runner) {
	r.Run()
}
