package migration

import (
	"context"
	"database/sql"
)

type SQLExecutor struct {
	DB *sql.DB
}

func (e SQLExecutor) Exec(ctx context.Context, statement string) error {
	_, err := e.DB.ExecContext(ctx, statement)
	return err
}
