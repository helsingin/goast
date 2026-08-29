package release

import (
	"bytes"
	"crypto/sha256"
	"errors"
)

type Permit struct {
	payload     [32]byte
	destination string
}

type Sink interface {
	Send(Permit, []byte, string) error
}

type AlternateSink interface {
	Publish([]byte, string) error
}

type Outbound struct{}

func (Outbound) Send(permit Permit, payload []byte, destination string) error {
	if permit.payload != sha256.Sum256(payload) || permit.destination != destination {
		return errors.New("invalid release permit")
	}
	return nil
}

func Authorize(payload []byte, destination string) (Permit, error) {
	if len(bytes.TrimSpace(payload)) == 0 || destination == "" {
		return Permit{}, errors.New("release denied")
	}
	return Permit{payload: sha256.Sum256(payload), destination: destination}, nil
}

func Run(kind string, sink Sink, payload []byte, destination string) error {
	switch kind {
	case "valid":
		return Valid(sink, payload, destination)
	case "bypass":
		return Bypass(sink, payload, destination)
	case "decorative":
		return Decorative(sink, payload, destination)
	case "mutated":
		return Mutated(sink, payload, destination)
	case "failure-open":
		return FailureOpen(sink, payload, destination)
	case "destination-mutated":
		return DestinationMutated(sink, payload, destination)
	case "reused":
		return Reused(sink, payload, destination)
	default:
		return errors.New("unknown path")
	}
}

func AlternateBypass(sink AlternateSink, payload []byte, destination string) error {
	return sink.Publish(payload, destination)
}

func Valid(sink Sink, payload []byte, destination string) error {
	permit, err := Authorize(payload, destination)
	if err != nil {
		return err
	}
	return sink.Send(permit, payload, destination)
}

func Bypass(sink Sink, payload []byte, destination string) error {
	return sink.Send(Permit{}, payload, destination)
}

func Decorative(sink Sink, payload []byte, destination string) error {
	_, _ = Authorize(payload, destination)
	return sink.Send(Permit{}, payload, destination)
}

func Mutated(sink Sink, payload []byte, destination string) error {
	permit, err := Authorize(payload, destination)
	if err != nil {
		return err
	}
	payload = append(payload, '!')
	return sink.Send(permit, payload, destination)
}

func FailureOpen(sink Sink, payload []byte, destination string) error {
	permit, err := Authorize(payload, destination)
	if err != nil {
		return sink.Send(permit, payload, destination)
	}
	return nil
}

func DestinationMutated(sink Sink, payload []byte, destination string) error {
	permit, err := Authorize(payload, destination)
	if err != nil {
		return err
	}
	destination += "-substituted"
	return sink.Send(permit, payload, destination)
}

func Reused(sink Sink, payload []byte, destination string) error {
	oldPayload := append([]byte(nil), payload...)
	permit, err := Authorize(oldPayload, destination)
	if err != nil {
		return err
	}
	return sink.Send(permit, payload, destination)
}
