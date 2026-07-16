//go:build !cgo

package worker

// NoCgoCall is active in the test's explicit cgo-disabled contexts.
func NoCgoCall(r Runner) {
	r.Run()
}
