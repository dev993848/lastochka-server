//go:build postgres
// +build postgres

package main

import (
	"context"
	"errors"

	"github.com/tinode/chat/server/db/postgres"
)

// ensureReactionsStorage creates the reactions table if missing (fork
// convention: feature tables via ensure*, not adapter version bumps).
func ensureReactionsStorage() error {
	db := postgres.CurrentDB()
	if db == nil {
		return errors.New("postgres connection is not initialized")
	}
	statements := []string{
		`CREATE TABLE IF NOT EXISTS reactions(
			topic  VARCHAR(25) NOT NULL,
			seqid  INT NOT NULL,
			userid BIGINT NOT NULL,
			emoji  VARCHAR(32) NOT NULL,
			PRIMARY KEY(topic, seqid, userid),
			FOREIGN KEY(topic) REFERENCES topics(name) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS reactions_topic_seqid ON reactions(topic, seqid)`,
	}
	for _, stmt := range statements {
		if _, err := db.Exec(context.Background(), stmt); err != nil {
			return errors.New("init reactions storage: " + err.Error())
		}
	}
	return nil
}
