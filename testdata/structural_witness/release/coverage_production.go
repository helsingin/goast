//go:build production

package release

func SplitCoverage(sink Sink, payload []byte, destination string) error {
	return Valid(sink, payload, destination)
}

func DefaultOnlySink(sink Sink, payload []byte, destination string) error {
	return nil
}
