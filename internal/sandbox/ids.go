package sandbox

import (
	"fmt"
	"sync/atomic"
)

var idCounter atomic.Uint64

func generateSessionID() string {
	n := idCounter.Add(1)
	return fmt.Sprintf("sess-%08x", n)
}
