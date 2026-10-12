package helpers

import (
	"fmt"
	"sync/atomic"
	"time"
)

var txnCounter atomic.Int64
var start = time.Now().Unix()

// GetTxnID generates a unique transaction ID for use with endpoints like /send/{eventType/{txnId}.
// The prefix is only for debugging purposes.
func GetTxnID(prefix string) (txnID string) {
	return fmt.Sprintf("%s-%d-%d", prefix, start, txnCounter.Add(1))
}
