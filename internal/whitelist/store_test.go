package whitelist_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"gh.tarampamp.am/video-dl-bot/internal/whitelist"
)

func TestStoreAddRemoveListPersist(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "whitelist.json")

	store, err := whitelist.NewStore(path)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	if store.IsAllowed(111) {
		t.Fatal("expected user 111 to be absent")
	}

	if err := store.Add(111, "alice"); err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	if err := store.Add(222, "bob"); err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	if !store.IsAllowed(111) || !store.IsAllowed(222) {
		t.Fatal("expected both users to be allowed")
	}

	users := store.List()
	if len(users) != 2 {
		t.Fatalf("expected 2 users, got %d", len(users))
	}

	if err := store.Remove(111); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}

	if store.IsAllowed(111) {
		t.Fatal("expected user 111 to be removed")
	}

	reloaded, err := whitelist.NewStore(path)
	if err != nil {
		t.Fatalf("reload NewStore() error = %v", err)
	}

	if reloaded.IsAllowed(111) {
		t.Fatal("expected user 111 to stay removed after reload")
	}

	if !reloaded.IsAllowed(222) {
		t.Fatal("expected user 222 to persist after reload")
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	var data struct {
		Users []whitelist.User `json:"users"`
	}

	if err := json.Unmarshal(content, &data); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if len(data.Users) != 1 || data.Users[0].ID != 222 {
		t.Fatalf("unexpected persisted users: %+v", data.Users)
	}
}

func TestStoreRemoveMissingUser(t *testing.T) {
	t.Parallel()

	store, err := whitelist.NewStore(filepath.Join(t.TempDir(), "whitelist.json"))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	if err := store.Remove(999); err == nil {
		t.Fatal("expected error when removing missing user")
	}
}
