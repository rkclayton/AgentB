package llm

import "errors"

type TransportKind string

const (
	TransportDial      TransportKind = "dial"
	TransportConnected TransportKind = "connected"
)

type TransportError struct {
	Kind TransportKind
	Err  error
}

func (e *TransportError) Error() string { return e.Err.Error() }
func (e *TransportError) Unwrap() error { return e.Err }

func TransportKindOf(err error) TransportKind {
	var transport *TransportError
	if errors.As(err, &transport) {
		return transport.Kind
	}
	return ""
}
