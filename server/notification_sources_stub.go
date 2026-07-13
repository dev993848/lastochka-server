//go:build !postgres
// +build !postgres

package main

import "errors"

var errNotificationSourceNotFound = errors.New("notification source not found")

func ensureNotificationSourcesStorage() error {
	return errors.New("notification sources require postgres build")
}

func listNotificationSourcesByUser(_ string) ([]notificationSourceListItem, error) {
	return nil, errors.New("notification sources require postgres build")
}

func createNotificationSourceRecord(_ string, _ string, _ string) (notificationSource, error) {
	return notificationSource{}, errors.New("notification sources require postgres build")
}

func updateNotificationSourceRecord(_, _ string, _ updateNotificationSourceRequest) (notificationSourceListItem, error) {
	return notificationSourceListItem{}, errors.New("notification sources require postgres build")
}

func rotateNotificationSourceRecord(_, _ string) (string, error) {
	return "", errors.New("notification sources require postgres build")
}

func deleteNotificationSourceRecord(_, _ string) error {
	return errors.New("notification sources require postgres build")
}

func findNotificationSourceByToken(_ string) (string, notificationSource, bool, error) {
	return "", notificationSource{}, false, errors.New("notification sources require postgres build")
}

func touchNotificationSourceLastUsed(_ string) error {
	return errors.New("notification sources require postgres build")
}
