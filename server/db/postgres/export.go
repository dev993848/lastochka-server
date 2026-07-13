//go:build postgres
// +build postgres

package postgres

import "github.com/jackc/pgx/v4/pgxpool"

var currentAdapter *adapter

// CurrentDB exposes the active pgx pool for server-local extensions.
func CurrentDB() *pgxpool.Pool {
	if currentAdapter == nil {
		return nil
	}
	return currentAdapter.db
}
