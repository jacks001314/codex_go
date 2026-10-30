package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// credentialStorageError keeps a stable operation context while preserving the
// typed cause. Its formatting mirrors Rust CredentialStorageError.
type credentialStorageError struct {
	context string
	cause   error
}

func (e *credentialStorageError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.cause == nil {
		return e.context
	}
	return e.context + ": " + e.cause.Error()
}

func (e *credentialStorageError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func (e *credentialStorageError) Format(state fmt.State, verb rune) {
	fmt.Fprint(state, e.Error())
}

func credentialStorageContext(context string, err error) error {
	if err == nil {
		return nil
	}
	return &credentialStorageError{context: context, cause: redactCredentialJSONError(nil, err)}
}

// credentialStorageJSONContext reports a JSON decode failure with only its
// classify/position metadata. Decode diagnostics may quote credential-bearing
// input, so the whole chain is discarded (Rust storage_error::with_context).
func credentialStorageJSONContext(context string, data []byte, err error) error {
	if err == nil {
		return nil
	}
	return &credentialStorageError{context: context, cause: redactCredentialJSONError(data, err)}
}

func redactCredentialJSONError(data []byte, err error) error {
	var syntax *json.SyntaxError
	if errors.As(err, &syntax) {
		line, column := jsonErrorPosition(data, syntax.Offset)
		return fmt.Errorf("invalid credential JSON (Syntax at line %d column %d)", line, column)
	}
	var typeError *json.UnmarshalTypeError
	if errors.As(err, &typeError) {
		line, column := jsonErrorPosition(data, typeError.Offset)
		return fmt.Errorf("invalid credential JSON (Data at line %d column %d)", line, column)
	}
	return err
}

// jsonErrorPosition converts encoding/json's 1-based byte offset into the
// 1-based line/column pair serde_json reports.
func jsonErrorPosition(data []byte, offset int64) (int, int) {
	if offset < 1 {
		offset = 1
	}
	if int64(len(data))+1 < offset {
		offset = int64(len(data)) + 1
	}
	prefix := data[:offset-1]
	line := 1 + bytes.Count(prefix, []byte("\n"))
	column := len(prefix) - bytes.LastIndexByte(prefix, '\n')
	return line, column
}
