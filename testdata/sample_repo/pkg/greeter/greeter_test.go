package greeter

import "testing"

func TestInternalGreeterGreet(t *testing.T) {
	g := NewGreeter("test")
	if got := callGreetFromInternalTest(g); got == "" {
		t.Fatal("empty greeting")
	}
	reset := (*Greeter).Reset
	reset(g)
}

func callGreetFromInternalTest(g *Greeter) string {
	return g.Greet("internal") + g.Greet("again")
}

func callPromotedMethodFromInternalTest(s *HappySpeaker) error {
	return s.Speak("hello")
}

func callInterfaceMethodFromInternalTest(s Speaker) error {
	return s.Speak("hello")
}
