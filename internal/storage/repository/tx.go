package repository

import "context"

type Tx interface {
	Context() context.Context
}

type TxManager interface {
	WithTx(ctx context.Context, fn func(ctx context.Context) error) error
}

type NoopTxManager struct{}

func (NoopTxManager) WithTx(ctx context.Context, fn func(ctx context.Context) error) error {
	return fn(ctx)
}
