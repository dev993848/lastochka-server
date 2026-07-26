//go:build !postgres
// +build !postgres

package main

import "errors"

var errInviteNotFound = errors.New("invites require postgres build")

type inviteConsumptionResult struct {
	SuspicionScore int
}

func inviteRegistrationRequired() bool {
	return false
}

func ensureInvitesStorage() error {
	return errors.New("invites require postgres build")
}

func listInvitesByUser(userID string) ([]inviteListItem, error) {
	return nil, errors.New("invites require postgres build")
}

func createInviteRecord(userID string, req createInviteRequest) (invite, error) {
	return invite{}, errors.New("invites require postgres build")
}

func previewInviteByCode(code string) (invitePreview, error) {
	return invitePreview{}, errInviteNotFound
}

func updateInviteRecord(userID, id string, req updateInviteRequest) (invite, error) {
	return invite{}, errors.New("invites require postgres build")
}

func revokeInviteRecord(userID, id string) error {
	return errors.New("invites require postgres build")
}

func getInviteGraphForUser(userID string) (map[string]any, error) {
	return nil, errors.New("invites require postgres build")
}

func consumeInviteForRegistration(inviteCode string, s *Session, user any, creds []MsgCredClient) (inviteConsumptionResult, error) {
	return inviteConsumptionResult{}, errors.New("invites require postgres build")
}
