package internal

import (
	"errors"
	"fmt"
)

// The exit codes lpm answers with. A host CLI branches on these, so they are
// part of the contract and only ever grow.
const (
	ExitOK         = 0
	ExitError      = 1
	ExitUsage      = 2
	ExitLockChange = 3
	ExitOffline    = 4
)

// ExitError carries the code a failure should leave the process with.
// Anything else is ExitError.
type CodedError struct {
	Code int
	Err  error
}

func (e *CodedError) Error() string { return e.Err.Error() }

func (e *CodedError) Unwrap() error { return e.Err }

// Fail builds an error that exits with code.
func Fail(code int, format string, args ...any) error {
	return &CodedError{Code: code, Err: fmt.Errorf(format, args...)}
}

// ExitCodeFor reads the code off an error, defaulting to a plain failure.
func ExitCodeFor(err error) int {
	if err == nil {
		return ExitOK
	}
	var coded *CodedError
	if errors.As(err, &coded) {
		return coded.Code
	}
	return ExitError
}
