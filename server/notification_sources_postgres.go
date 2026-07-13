//go:build postgres
// +build postgres

package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/tinode/chat/server/db/postgres"
)

var errNotificationSourceNotFound = errors.New("notification source not found")

func ensureNotificationSourcesStorage() error {
	db := postgres.CurrentDB()
	if db == nil {
		return errors.New("postgres connection is not initialized")
	}

	_, err := db.Exec(context.Background(), `
		CREATE TABLE IF NOT EXISTS notification_sources (
			id VARCHAR(32) PRIMARY KEY,
			user_id VARCHAR(32) NOT NULL,
			name TEXT NOT NULL,
			topic_name TEXT NOT NULL,
			enabled BOOLEAN NOT NULL DEFAULT TRUE,
			token VARCHAR(64) NOT NULL UNIQUE,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			last_used_at TIMESTAMPTZ NULL
		)`)
	if err != nil {
		return fmt.Errorf("create notification_sources table: %w", err)
	}

	_, err = db.Exec(context.Background(), `CREATE INDEX IF NOT EXISTS idx_notification_sources_user_updated_at ON notification_sources(user_id, updated_at DESC)`)
	if err != nil {
		return fmt.Errorf("create notification_sources user index: %w", err)
	}

	return nil
}

func listNotificationSourcesByUser(userID string) ([]notificationSourceListItem, error) {
	db := postgres.CurrentDB()
	if db == nil {
		return nil, errors.New("postgres connection is not initialized")
	}

	rows, err := db.Query(context.Background(), `
		SELECT id, name, topic_name, enabled, created_at, updated_at, last_used_at
		FROM notification_sources
		WHERE user_id = $1
		ORDER BY updated_at DESC, created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]notificationSourceListItem, 0)
	for rows.Next() {
		var item notificationSourceListItem
		if err := rows.Scan(&item.ID, &item.Name, &item.TopicName, &item.Enabled, &item.CreatedAt, &item.UpdatedAt, &item.LastUsedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return items, nil
}

func createNotificationSourceRecord(userID string, name string, topicName string) (notificationSource, error) {
	db := postgres.CurrentDB()
	if db == nil {
		return notificationSource{}, errors.New("postgres connection is not initialized")
	}

	id, err := randomHex(8)
	if err != nil {
		return notificationSource{}, err
	}
	token, err := randomHex(16)
	if err != nil {
		return notificationSource{}, err
	}

	item := notificationSource{ID: id, Name: name, TopicName: topicName, Enabled: true, Token: token}
	err = db.QueryRow(context.Background(), `
		INSERT INTO notification_sources (id, user_id, name, topic_name, enabled, token)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING created_at, updated_at, last_used_at`,
		item.ID, userID, item.Name, item.TopicName, item.Enabled, item.Token,
	).Scan(&item.CreatedAt, &item.UpdatedAt, &item.LastUsedAt)
	if err != nil {
		return notificationSource{}, err
	}

	return item, nil
}

func updateNotificationSourceRecord(userID, id string, req updateNotificationSourceRequest) (notificationSourceListItem, error) {
	db := postgres.CurrentDB()
	if db == nil {
		return notificationSourceListItem{}, errors.New("postgres connection is not initialized")
	}

	ctx := context.Background()
	var current notificationSource
	err := db.QueryRow(ctx, `
		SELECT id, name, topic_name, enabled, token, created_at, updated_at, last_used_at
		FROM notification_sources
		WHERE user_id = $1 AND id = $2`, userID, id,
	).Scan(&current.ID, &current.Name, &current.TopicName, &current.Enabled, &current.Token, &current.CreatedAt, &current.UpdatedAt, &current.LastUsedAt)
	if err != nil {
		return notificationSourceListItem{}, errNotificationSourceNotFound
	}

	if req.Name != nil {
		current.Name = *req.Name
	}
	if req.Enabled != nil {
		current.Enabled = *req.Enabled
	}

	current.UpdatedAt = time.Now().UTC()
	err = db.QueryRow(ctx, `
		UPDATE notification_sources
		SET name = $3, enabled = $4, updated_at = $5
		WHERE user_id = $1 AND id = $2
		RETURNING created_at, last_used_at`,
		userID, id, current.Name, current.Enabled, current.UpdatedAt,
	).Scan(&current.CreatedAt, &current.LastUsedAt)
	if err != nil {
		return notificationSourceListItem{}, err
	}

	return toNotificationSourceListItem(current), nil
}

func rotateNotificationSourceRecord(userID, id string) (string, error) {
	db := postgres.CurrentDB()
	if db == nil {
		return "", errors.New("postgres connection is not initialized")
	}

	newToken, err := randomHex(16)
	if err != nil {
		return "", err
	}
	updatedAt := time.Now().UTC()
	commandTag, err := db.Exec(context.Background(), `
		UPDATE notification_sources
		SET token = $3, updated_at = $4
		WHERE user_id = $1 AND id = $2`,
		userID, id, newToken, updatedAt,
	)
	if err != nil {
		return "", err
	}
	if commandTag.RowsAffected() == 0 {
		return "", errNotificationSourceNotFound
	}

	return newToken, nil
}

func deleteNotificationSourceRecord(userID, id string) error {
	db := postgres.CurrentDB()
	if db == nil {
		return errors.New("postgres connection is not initialized")
	}

	commandTag, err := db.Exec(context.Background(), `DELETE FROM notification_sources WHERE user_id = $1 AND id = $2`, userID, id)
	if err != nil {
		return err
	}
	if commandTag.RowsAffected() == 0 {
		return errNotificationSourceNotFound
	}
	return nil
}

func findNotificationSourceByToken(token string) (string, notificationSource, bool, error) {
	db := postgres.CurrentDB()
	if db == nil {
		return "", notificationSource{}, false, errors.New("postgres connection is not initialized")
	}

	var ownerID string
	var item notificationSource
	err := db.QueryRow(context.Background(), `
		SELECT user_id, id, name, topic_name, enabled, token, created_at, updated_at, last_used_at
		FROM notification_sources
		WHERE token = $1`, token,
	).Scan(&ownerID, &item.ID, &item.Name, &item.TopicName, &item.Enabled, &item.Token, &item.CreatedAt, &item.UpdatedAt, &item.LastUsedAt)
	if err != nil {
		return "", notificationSource{}, false, nil
	}

	return ownerID, item, true, nil
}

func touchNotificationSourceLastUsed(id string) error {
	db := postgres.CurrentDB()
	if db == nil {
		return errors.New("postgres connection is not initialized")
	}
	_, err := db.Exec(context.Background(), `UPDATE notification_sources SET last_used_at = NOW(), updated_at = NOW() WHERE id = $1`, id)
	return err
}
