package runner

import (
	"context"
	"fmt"
	"time"
)

type Request struct {
	RunID      string
	Prompt     string
	WorkingDir string
}

type Output struct {
	Stream string
	Text   string
}

type Result struct {
	ExitCode int
	Duration time.Duration
	Summary  string
}

type Runner interface {
	Run(context.Context, Request, func(Output)) (Result, error)
}

type StartError struct {
	Err error
}

func (e *StartError) Error() string {
	return fmt.Sprintf("runner failed to start: %v", e.Err)
}

func (e *StartError) Unwrap() error {
	return e.Err
}

type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("runner exited with code %d: %v", e.Code, e.Err)
}

func (e *ExitError) Unwrap() error {
	return e.Err
}
