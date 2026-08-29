//go:build production

package release

func ProductionOnly(sink Sink, payload []byte, destination string) error {
	return Valid(sink, payload, destination)
}
