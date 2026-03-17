package helpers

import (
	"fmt"
	"sync/atomic"
	"time"
)

var txnCounter atomic.Int64
var start = time.Now().Unix()

func GetTxnID(prefix string) (txnID string) {
	return fmt.Sprintf("%s-%d-%d", prefix, start, txnCounter.Add(1))
}
