package repository

import (
	"errors"

	"znt/internal/contracts"
)

var (
	ErrNotFound         = errors.New("repository: not found")
	ErrConflict         = contracts.NewRuntimeError(contracts.CodeTaskConflict, "optimistic lock conflict", nil)
	ErrDuplicateRequest = errors.New("repository: duplicate idempotency key")
)

func IsConflict(err error) bool {
	var runtimeErr *contracts.RuntimeError
	if errors.As(err, &runtimeErr) {
		return runtimeErr.Code == contracts.CodeTaskConflict
	}
	return false
}
