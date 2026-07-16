package greeter_test

import (
	"testing"

	"example.com/sample/pkg/greeter"
)

func TestExternalGreeterGreet(t *testing.T) {
	g := greeter.NewGreeter("external")
	if got := callGreetFromExternalTest(g); got == "" {
		t.Fatal("empty greeting")
	}
}

func callGreetFromExternalTest(g *greeter.Greeter) string {
	return g.Greet("external")
}
