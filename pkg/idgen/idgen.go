package idgen

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"sync/atomic"
	"time"
)

var (
	readRandom      = rand.Read
	fallbackCounter uint64
)

func New(prefix string) string {
	var b [16]byte
	if _, err := readRandom(b[:]); err != nil {
		seed := fmt.Sprintf("%s:%d:%d:%d", prefix, time.Now().UnixNano(), os.Getpid(), atomic.AddUint64(&fallbackCounter, 1))
		sum := sha256.Sum256([]byte(seed))
		copy(b[:], sum[:16])
	}
	return prefix + "_" + hex.EncodeToString(b[:])
}
