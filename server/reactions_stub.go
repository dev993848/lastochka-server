//go:build !postgres
// +build !postgres

package main

import "errors"

func ensureReactionsStorage() error {
	return errors.New("reactions require postgres build")
}
