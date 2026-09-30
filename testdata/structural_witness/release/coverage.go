package release

func OtherSend(Permit, []byte, string) error {
	return nil
}

func ValidOther(payload []byte, destination string) error {
	permit, err := Authorize(payload, destination)
	if err != nil {
		return err
	}
	return OtherSend(permit, payload, destination)
}

func BothSinks(sink Sink, payload []byte, destination string) error {
	if err := Valid(sink, payload, destination); err != nil {
		return err
	}
	return ValidOther(payload, destination)
}
