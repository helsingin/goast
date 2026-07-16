package worker

// Run is the Darwin implementation.
func (Runner) Run() {}

// DarwinCall exists only in the Darwin build context.
func DarwinCall(r Runner) {
	r.Run()
}

// PlatformCaller deliberately has the same identity as its Linux variant.
func (r Runner) PlatformCaller() {
	r.Run()
}
