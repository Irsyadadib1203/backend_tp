package provider

import "fmt"

type ErrorKind string

const (
	ErrorTemporary      ErrorKind = "temporary"
	ErrorFinal          ErrorKind = "final"
	ErrorHold           ErrorKind = "hold"
	ErrorConfiguration  ErrorKind = "configuration"
	ErrorAuthentication ErrorKind = "authentication"
	ErrorBalance        ErrorKind = "balance"
	ErrorTimeout        ErrorKind = "timeout"
)

// ProviderError normalizes a provider failure. Status is the business
// consequence (hold/final); Kind is diagnostic only.
type ProviderError struct {
	Status              Status
	Kind                ErrorKind
	ProviderStatus      string
	Message             string
	Cause               error
	Raw                 []byte
	RequestRaw          []byte
	PersistBeforeStatus bool
}

func (e *ProviderError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return fmt.Sprintf("provider error: %s", e.ProviderStatus)
}

func (e *ProviderError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}
