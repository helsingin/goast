// Package app demonstrates cross-package references into greeter so the
// reference-index tests have something concrete to resolve against.
package app

import "example.com/sample/pkg/greeter"

// Run creates a greeter and formats a message. It produces two cross-package
// call references: greeter.NewGreeter and greeter.FormatGreeting.
func Run(name string) string {
	g := greeter.NewGreeter("Hi")
	return greeter.FormatGreeting(g.Greet(name), name)
}

// helper calls Run to produce a same-package unqualified reference.
func helper() string {
	return Run("world")
}
