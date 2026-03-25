package adapter

import "errors"

const (
	StreamErrorStageUpstreamRead    = "upstream_read"
	StreamErrorStageDownstreamWrite = "downstream_write"
)

type StreamIOError struct {
	Stage string
	Err   error
}

func (e *StreamIOError) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}
	return e.Stage + ": " + e.Err.Error()
}

func (e *StreamIOError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func WrapUpstreamReadError(err error) error {
	if err == nil {
		return nil
	}
	return &StreamIOError{Stage: StreamErrorStageUpstreamRead, Err: err}
}

func WrapDownstreamWriteError(err error) error {
	if err == nil {
		return nil
	}
	return &StreamIOError{Stage: StreamErrorStageDownstreamWrite, Err: err}
}

func StreamErrorStageOf(err error) string {
	var streamErr *StreamIOError
	if errors.As(err, &streamErr) {
		return streamErr.Stage
	}
	return ""
}
