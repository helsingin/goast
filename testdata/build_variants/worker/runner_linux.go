package worker

import "runtime"

// Run is the Linux implementation.
func (Runner) Run() {}

// LinuxCall exists only in the Linux build context.
func LinuxCall(r Runner) {
	r.Run()
	_ = runtime.GOOS
}

// PlatformCaller deliberately has the same identity as its Darwin variant.
func (r Runner) PlatformCaller() {
	r.Run()
}
