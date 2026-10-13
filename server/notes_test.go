package main

import (
	"sync"
	"testing"
)

// newTestNoteStore returns an empty store isolated from the global notes var.
func newTestNoteStore() *noteStore {
	return &noteStore{items: make(map[string]map[string]note)}
}

func strptr(s string) *string { return &s }

func TestNoteStoreCreate(t *testing.T) {
	s := newTestNoteStore()

	item, err := s.create("usrTest1", "  Title  ", " body ")
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if item.ID == "" {
		t.Error("expected non-empty id")
	}
	if item.Title != "Title" {
		t.Errorf("title not trimmed, got %q", item.Title)
	}
	if item.Content != "body" {
		t.Errorf("content not trimmed, got %q", item.Content)
	}
	if item.CreatedAt.IsZero() || item.UpdatedAt.IsZero() {
		t.Error("expected timestamps to be set")
	}

	if _, err := s.create("usrTest1", "   ", "x"); err != errNoteTitleRequired {
		t.Errorf("empty title: expected errNoteTitleRequired, got %v", err)
	}
	if _, err := s.create("usrTest1", "", "x"); err != errNoteTitleRequired {
		t.Errorf("missing title: expected errNoteTitleRequired, got %v", err)
	}
}

func TestNoteStoreListIsolation(t *testing.T) {
	s := newTestNoteStore()

	if got := s.list("usrNobody"); len(got) != 0 {
		t.Fatalf("expected empty list, got %d items", len(got))
	}

	if _, err := s.create("usrA", "a1", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.create("usrA", "a2", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.create("usrB", "b1", ""); err != nil {
		t.Fatal(err)
	}

	if got := s.list("usrA"); len(got) != 2 {
		t.Errorf("usrA: expected 2 notes, got %d", len(got))
	}
	if got := s.list("usrB"); len(got) != 1 {
		t.Errorf("usrB: expected 1 note, got %d", len(got))
	}
}

func TestNoteStoreUpdate(t *testing.T) {
	s := newTestNoteStore()
	created, err := s.create("usrU", "orig", "orig body")
	if err != nil {
		t.Fatal(err)
	}

	// Title only.
	updated, err := s.update("usrU", created.ID, strptr("new title"), nil)
	if err != nil {
		t.Fatalf("update title failed: %v", err)
	}
	if updated.Title != "new title" || updated.Content != "orig body" {
		t.Errorf("unexpected partial update: %+v", updated)
	}

	// Content only.
	updated, err = s.update("usrU", created.ID, nil, strptr("new body"))
	if err != nil {
		t.Fatalf("update content failed: %v", err)
	}
	if updated.Title != "new title" || updated.Content != "new body" {
		t.Errorf("unexpected partial update: %+v", updated)
	}

	// Empty title is rejected, note stays intact.
	if _, err := s.update("usrU", created.ID, strptr("  "), nil); err != errNoteTitleRequired {
		t.Errorf("expected errNoteTitleRequired, got %v", err)
	}

	// Unknown id and foreign user.
	if _, err := s.update("usrU", "nope", strptr("x"), nil); err != errNoteNotFound {
		t.Errorf("expected errNoteNotFound, got %v", err)
	}
	if _, err := s.update("usrOther", created.ID, strptr("x"), nil); err != errNoteNotFound {
		t.Errorf("cross-user update: expected errNoteNotFound, got %v", err)
	}
}

func TestNoteStoreDelete(t *testing.T) {
	s := newTestNoteStore()
	created, err := s.create("usrD", "t", "c")
	if err != nil {
		t.Fatal(err)
	}

	if err := s.delete("usrD", created.ID); err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	if err := s.delete("usrD", created.ID); err != errNoteNotFound {
		t.Errorf("second delete: expected errNoteNotFound, got %v", err)
	}
	if err := s.delete("usrOther", created.ID); err != errNoteNotFound {
		t.Errorf("cross-user delete: expected errNoteNotFound, got %v", err)
	}
	if got := s.list("usrD"); len(got) != 0 {
		t.Errorf("expected empty list after delete, got %d", len(got))
	}
}

func TestNoteStoreConcurrent(t *testing.T) {
	s := newTestNoteStore()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 25; j++ {
				item, err := s.create("usrC", "t", "c")
				if err != nil {
					t.Errorf("concurrent create: %v", err)
					return
				}
				_ = s.list("usrC")
				_ = s.delete("usrC", item.ID)
			}
		}()
	}
	wg.Wait()
	if got := s.list("usrC"); len(got) != 0 {
		t.Errorf("expected all concurrent notes deleted, left %d", len(got))
	}
}
