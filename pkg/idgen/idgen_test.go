package idgen

import (
	"errors"
	"strings"
	"testing"
)

func TestNewFallsBackWhenRandomReadFails(t *testing.T) {
	original := readRandom
	readRandom = func([]byte) (int, error) {
		return 0, errors.New("random unavailable")
	}
	defer func() { readRandom = original }()

	id := New("test")
	if !strings.HasPrefix(id, "test_") || len(id) != len("test_")+32 {
		t.Fatalf("unexpected fallback id %q", id)
	}
}
