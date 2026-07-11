package whitelist

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

// User represents a whitelisted Telegram user.
type User struct {
	ID       int64     `json:"id"`
	Username string    `json:"username,omitempty"`
	AddedAt  time.Time `json:"added_at"`
}

type fileData struct {
	Users []User `json:"users"`
}

// Store persists allowed user IDs in a JSON file.
type Store struct {
	mu   sync.RWMutex
	path string
	data fileData
}

// NewStore loads an existing whitelist file or creates an empty one.
func NewStore(path string) (*Store, error) {
	store := &Store{
		path: path,
		data: fileData{Users: []User{}},
	}

	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return store, store.save()
		}

		return nil, fmt.Errorf("read whitelist file: %w", err)
	}

	if len(content) > 0 {
		if err := json.Unmarshal(content, &store.data); err != nil {
			return nil, fmt.Errorf("parse whitelist file: %w", err)
		}
	}

	if store.data.Users == nil {
		store.data.Users = []User{}
	}

	return store, nil
}

// IsAllowed reports whether the user ID is in the whitelist.
func (s *Store) IsAllowed(userID int64) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, user := range s.data.Users {
		if user.ID == userID {
			return true
		}
	}

	return false
}

// Add inserts a user into the whitelist.
func (s *Store) Add(userID int64, username string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, user := range s.data.Users {
		if user.ID == userID {
			if username != "" && user.Username != username {
				s.data.Users[i].Username = username
				return s.saveLocked()
			}

			return nil
		}
	}

	s.data.Users = append(s.data.Users, User{
		ID:       userID,
		Username: username,
		AddedAt:  time.Now().UTC(),
	})

	return s.saveLocked()
}

// Remove deletes a user from the whitelist.
func (s *Store) Remove(userID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx := slices.IndexFunc(s.data.Users, func(user User) bool {
		return user.ID == userID
	})
	if idx < 0 {
		return errors.New("user not found in whitelist")
	}

	s.data.Users = append(s.data.Users[:idx], s.data.Users[idx+1:]...)

	return s.saveLocked()
}

// List returns a copy of all whitelisted users sorted by ID.
func (s *Store) List() []User {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]User, len(s.data.Users))
	copy(out, s.data.Users)

	slices.SortFunc(out, func(a, b User) int {
		switch {
		case a.ID < b.ID:
			return -1
		case a.ID > b.ID:
			return 1
		default:
			return 0
		}
	})

	return out
}

func (s *Store) save() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.saveLocked()
}

func (s *Store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil { //nolint:mnd
		return fmt.Errorf("create whitelist directory: %w", err)
	}

	content, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return fmt.Errorf("encode whitelist file: %w", err)
	}

	tmpPath := s.path + ".tmp"

	if err := os.WriteFile(tmpPath, content, 0o600); err != nil { //nolint:mnd,gosec
		return fmt.Errorf("write whitelist temp file: %w", err)
	}

	if err := os.Rename(tmpPath, s.path); err != nil {
		_ = os.Remove(tmpPath)

		return fmt.Errorf("replace whitelist file: %w", err)
	}

	return nil
}
