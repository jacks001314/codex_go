package tool

import "strings"

type ErrorKind string

const (
	ErrorRespondToModel ErrorKind = "respond_to_model"
	ErrorFatal          ErrorKind = "fatal"
)

type FunctionCallError struct {
	Kind    ErrorKind
	Message string
}

func RespondToModel(message string) *FunctionCallError {
	return &FunctionCallError{Kind: ErrorRespondToModel, Message: message}
}

func Fatal(message string) *FunctionCallError {
	return &FunctionCallError{Kind: ErrorFatal, Message: message}
}

func FromError(err error) *FunctionCallError {
	if err == nil {
		return nil
	}
	var existing *FunctionCallError
	if AsFunctionCallError(err, &existing) {
		return existing
	}
	return Fatal(err.Error())
}

func (e *FunctionCallError) Error() string {
	if e == nil {
		return ""
	}
	if e.Kind == ErrorFatal {
		return "Fatal error: " + e.Message
	}
	return e.Message
}

func (e *FunctionCallError) RespondsToModel() bool {
	return e != nil && e.Kind == ErrorRespondToModel
}

func (e *FunctionCallError) IsFatal() bool {
	return e != nil && e.Kind == ErrorFatal
}

func (e *FunctionCallError) ModelMessage() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func (e *FunctionCallError) Is(target error) bool {
	other, ok := target.(*FunctionCallError)
	if !ok {
		return false
	}
	if other.Kind != "" && e.Kind != other.Kind {
		return false
	}
	return other.Message == "" || strings.Contains(e.Message, other.Message)
}

func AsFunctionCallError(err error, target **FunctionCallError) bool {
	if target == nil {
		return false
	}
	if value, ok := err.(*FunctionCallError); ok {
		*target = value
		return true
	}
	type unwrapper interface {
		Unwrap() error
	}
	if wrapped, ok := err.(unwrapper); ok {
		return AsFunctionCallError(wrapped.Unwrap(), target)
	}
	return false
}
