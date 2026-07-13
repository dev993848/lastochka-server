//go:build postgres
// +build postgres

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/tinode/chat/server/db/postgres"
)

var errNotificationEventNotFound = errors.New("notification event not found")

func ensureNotificationEventsStorage() error {
	db := postgres.CurrentDB()
	if db == nil {
		return errors.New("postgres connection is not initialized")
	}

	_, err := db.Exec(context.Background(), `
  CREATE TABLE IF NOT EXISTS notification_events (
   id VARCHAR(32) PRIMARY KEY,
   user_id VARCHAR(32) NOT NULL,
   source_id VARCHAR(32) NOT NULL,
   source_name TEXT NOT NULL,
   title TEXT NOT NULL,
   body TEXT NOT NULL DEFAULT '',
   payload JSONB NOT NULL DEFAULT '{}'::jsonb,
   created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
   read_at TIMESTAMPTZ NULL
  )`)
	if err != nil {
		return fmt.Errorf("create notification_events table: %w", err)
	}

	_, err = db.Exec(context.Background(), `CREATE INDEX IF NOT EXISTS idx_notification_events_user_created_at ON notification_events(user_id, created_at DESC)`)
	if err != nil {
		return fmt.Errorf("create notification_events user index: %w", err)
	}

	return nil
}

func createNotificationEvent(userID string, source notificationSource, title string, body string, payload json.RawMessage) (notificationEvent, error) {
	db := postgres.CurrentDB()
	if db == nil {
		return notificationEvent{}, errors.New("postgres connection is not initialized")
	}

	id, err := randomHex(12)
	if err != nil {
		return notificationEvent{}, err
	}

	item := notificationEvent{
		ID:         id,
		SourceID:   source.ID,
		SourceName: source.Name,
		Title:      title,
		Body:       body,
		Payload:    payload,
	}
	var readAt sql.NullTime

	err = db.QueryRow(
		context.Background(),
		`INSERT INTO notification_events (id, user_id, source_id, source_name, title, body, payload)
   VALUES ($1, $2, $3, $4, $5, $6, $7)
   RETURNING created_at, read_at`,
		item.ID,
		userID,
		item.SourceID,
		item.SourceName,
		item.Title,
		item.Body,
		[]byte(item.Payload),
	).Scan(&item.CreatedAt, &readAt)
	if err != nil {
		return notificationEvent{}, err
	}
	item.ReadAt = nullableTimePtr(readAt)

	return item, nil
}

func listNotificationEvents(userID string, limit int, unreadOnly bool) ([]notificationEvent, int, error) {
	db := postgres.CurrentDB()
	if db == nil {
		return nil, 0, errors.New("postgres connection is not initialized")
	}

	var unreadCount int
	if err := db.QueryRow(context.Background(), `SELECT COUNT(*) FROM notification_events WHERE user_id = $1 AND read_at IS NULL`, userID).Scan(&unreadCount); err != nil {
		return nil, 0, err
	}

	query := `SELECT id, source_id, source_name, title, body, payload, created_at, read_at
	   FROM notification_events
	   WHERE user_id = $1`
	if unreadOnly {
		query += ` AND read_at IS NULL`
	}
	query += ` ORDER BY created_at DESC LIMIT $2`

	rows, err := db.Query(
		context.Background(),
		query,
		userID,
		limit,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	items := make([]notificationEvent, 0, limit)
	for rows.Next() {
		var item notificationEvent
		var payload []byte
		var readAt sql.NullTime
		if err := rows.Scan(&item.ID, &item.SourceID, &item.SourceName, &item.Title, &item.Body, &payload, &item.CreatedAt, &readAt); err != nil {
			return nil, 0, err
		}
		item.Payload = json.RawMessage(payload)
		item.ReadAt = nullableTimePtr(readAt)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	return items, unreadCount, nil
}

func markNotificationEventRead(userID, id string) (notificationEvent, error) {
	db := postgres.CurrentDB()
	if db == nil {
		return notificationEvent{}, errors.New("postgres connection is not initialized")
	}

	var item notificationEvent
	var payload []byte
	var readAt sql.NullTime
	err := db.QueryRow(
		context.Background(),
		`UPDATE notification_events
		 SET read_at = COALESCE(read_at, NOW())
		 WHERE user_id = $1 AND id = $2
		 RETURNING id, source_id, source_name, title, body, payload, created_at, read_at`,
		userID,
		id,
	).Scan(&item.ID, &item.SourceID, &item.SourceName, &item.Title, &item.Body, &payload, &item.CreatedAt, &readAt)
	if err != nil {
		return notificationEvent{}, errNotificationEventNotFound
	}
	item.Payload = json.RawMessage(payload)
	item.ReadAt = nullableTimePtr(readAt)
	return item, nil
}

func deleteNotificationEvent(userID, id string) error {
	db := postgres.CurrentDB()
	if db == nil {
		return errors.New("postgres connection is not initialized")
	}

	commandTag, err := db.Exec(context.Background(), `DELETE FROM notification_events WHERE user_id = $1 AND id = $2`, userID, id)
	if err != nil {
		return err
	}
	if commandTag.RowsAffected() == 0 {
		return errNotificationEventNotFound
	}
	return nil
}

func markAllNotificationEventsRead(userID string) (int64, error) {
	db := postgres.CurrentDB()
	if db == nil {
		return 0, errors.New("postgres connection is not initialized")
	}

	commandTag, err := db.Exec(context.Background(), `UPDATE notification_events SET read_at = COALESCE(read_at, NOW()) WHERE user_id = $1 AND read_at IS NULL`, userID)
	if err != nil {
		return 0, err
	}
	return commandTag.RowsAffected(), nil
}

func deleteReadNotificationEvents(userID string) (int64, error) {
	db := postgres.CurrentDB()
	if db == nil {
		return 0, errors.New("postgres connection is not initialized")
	}

	commandTag, err := db.Exec(context.Background(), `DELETE FROM notification_events WHERE user_id = $1 AND read_at IS NOT NULL`, userID)
	if err != nil {
		return 0, err
	}
	return commandTag.RowsAffected(), nil
}
