package logger

import "testing"

func TestLogDefaultsToNoopLogger(t *testing.T) {
	if Log == nil {
		t.Fatal("expected default logger to be initialized")
	}

	Log.Info("noop logger should not panic")
}
