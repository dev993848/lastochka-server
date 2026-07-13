//go:build !postgres
// +build !postgres

package main

import (
	"encoding/json"
	"errors"
)

var errNotificationEventNotFound = errors.New("notification event not found")

func ensureNotificationEventsStorage() error {
	return errors.New("notification events require postgres build")
}

func createNotificationEvent(_ string, _ notificationSource, _ string, _ string, _ json.RawMessage) (notificationEvent, error) {
	return notificationEvent{}, errors.New("notification events require postgres build")
}

func listNotificationEvents(_ string, _ int, _ bool) ([]notificationEvent, int, error) {
	return nil, 0, errors.New("notification events require postgres build")
}

func markNotificationEventRead(_, _ string) (notificationEvent, error) {
	return notificationEvent{}, errors.New("notification events require postgres build")
}

func deleteNotificationEvent(_, _ string) error {
	return errors.New("notification events require postgres build")
}

func markAllNotificationEventsRead(_ string) (int64, error) {
	return 0, errors.New("notification events require postgres build")
}

func deleteReadNotificationEvents(_ string) (int64, error) {
	return 0, errors.New("notification events require postgres build")
}
