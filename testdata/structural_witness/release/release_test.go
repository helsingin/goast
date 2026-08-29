package release

func TestOnlyEntry(sink Sink, payload []byte, destination string) error {
	return Valid(sink, payload, destination)
}
