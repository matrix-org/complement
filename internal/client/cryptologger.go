package client

import (
	"fmt"
	"testing"
)

type testCryptoLogger struct {
	t      *testing.T
	userID string
}

func (e testCryptoLogger) Error(message string, args ...interface{}) {
	e.t.Helper()
	e.t.Errorf("[%s] ERROR: "+fmt.Sprintf(message, args...), e.userID)
}
func (e testCryptoLogger) Warn(message string, args ...interface{}) {
	e.t.Helper()
	e.t.Logf("[%s] WARN:"+fmt.Sprintf(message, args...), e.userID)
}
func (e testCryptoLogger) Debug(message string, args ...interface{}) {
	e.t.Helper()
	e.t.Logf("[%s] DEBUG: "+fmt.Sprintf(message, args...), e.userID)
}
func (e testCryptoLogger) Trace(message string, args ...interface{}) {
	e.t.Helper()
	e.t.Logf("[%s] TRACE: "+fmt.Sprintf(message, args...), e.userID)
}

type emptyCryptoLogger struct{}

func (e emptyCryptoLogger) Error(message string, args ...interface{}) {}
func (e emptyCryptoLogger) Warn(message string, args ...interface{})  {}
func (e emptyCryptoLogger) Debug(message string, args ...interface{}) {}
func (e emptyCryptoLogger) Trace(message string, args ...interface{}) {}
