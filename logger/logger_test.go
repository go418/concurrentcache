package logger

import (
	"context"
	"fmt"
	"testing"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/funcr"
)

func Test_NewWorkerContextWithLogger(t *testing.T) {
	{
		ctx := context.Background()
		newCtx, _ := NewWorkerContextWithLogger(ctx)
		_, err := logr.FromContext(newCtx)
		if err == nil {
			t.Error("expected error to be thrown")
		}
	}

	{
		ctx := context.Background()
		logMessages := ""
		ctx = logr.NewContext(ctx, funcr.New(func(prefix, args string) {
			logMessages += args + "\n"
		}, funcr.Options{}))
		newCtx, detach := NewWorkerContextWithLogger(ctx)
		logger, err := logr.FromContext(newCtx)
		if err != nil {
			t.Error("expected error to be nil")
		}

		logger.Info("test message", "key1", "value1")
		logger.Error(fmt.Errorf("test message"), "test message", "key1", "value1")

		detach()

		logger.Info("test message", "key1", "value1")
		logger.Error(fmt.Errorf("test message"), "test message", "key1", "value1")

		expectedLogMessages := `"level"=0 "msg"="test message" "worker"="attached" "key1"="value1"
"msg"="test message" "error"="test message" "worker"="attached" "key1"="value1"
"level"=0 "msg"="test message" "worker"="detached" "key1"="value1"
"msg"="test message" "error"="test message" "worker"="detached" "key1"="value1"
`
		if logMessages != expectedLogMessages {
			t.Errorf("expected log messages %q to match expected %q", logMessages, expectedLogMessages)
		}
	}
}
