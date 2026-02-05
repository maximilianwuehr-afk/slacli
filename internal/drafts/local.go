package drafts

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"

	"slacli/internal/output"
)

// LocalStore manages local draft storage
type LocalStore struct {
	path   string
	mu     sync.RWMutex
	drafts map[string]*LocalDraft
}

// LocalDraft represents a locally stored draft
type LocalDraft struct {
	ID        string `json:"id"`
	ChannelID string `json:"channel_id"`
	Text      string `json:"text"`
	ThreadTS  string `json:"thread_ts,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// NewLocalStore creates a new local draft store
func NewLocalStore(storeDir string) (*LocalStore, error) {
	path := filepath.Join(storeDir, "drafts.json")
	store := &LocalStore{
		path:   path,
		drafts: make(map[string]*LocalDraft),
	}

	// Load existing drafts
	if err := store.load(); err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	return store, nil
}

func (s *LocalStore) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}

	var drafts []*LocalDraft
	if err := json.Unmarshal(data, &drafts); err != nil {
		return err
	}

	s.drafts = make(map[string]*LocalDraft)
	for _, d := range drafts {
		s.drafts[d.ID] = d
	}

	return nil
}

func (s *LocalStore) save() error {
	drafts := make([]*LocalDraft, 0, len(s.drafts))
	for _, d := range s.drafts {
		drafts = append(drafts, d)
	}

	data, err := json.MarshalIndent(drafts, "", "  ")
	if err != nil {
		return err
	}

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return err
	}

	return os.WriteFile(s.path, data, 0644)
}

// List returns all local drafts
func (s *LocalStore) List(limit int) []output.Draft {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]output.Draft, 0, len(s.drafts))
	for _, d := range s.drafts {
		result = append(result, output.Draft{
			ID:        d.ID,
			ChannelID: d.ChannelID,
			Text:      d.Text,
			ThreadTS:  d.ThreadTS,
			CreatedAt: d.CreatedAt,
			UpdatedAt: d.UpdatedAt,
		})
		if limit > 0 && len(result) >= limit {
			break
		}
	}

	return result
}

// Create creates a new local draft
func (s *LocalStore) Create(channelID, text, threadTS string) (output.Draft, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().Format(time.RFC3339)
	draft := &LocalDraft{
		ID:        uuid.New().String()[:8],
		ChannelID: channelID,
		Text:      text,
		ThreadTS:  threadTS,
		CreatedAt: now,
		UpdatedAt: now,
	}

	s.drafts[draft.ID] = draft

	if err := s.save(); err != nil {
		return output.Draft{}, fmt.Errorf("save draft: %w", err)
	}

	return output.Draft{
		ID:        draft.ID,
		ChannelID: draft.ChannelID,
		Text:      draft.Text,
		ThreadTS:  draft.ThreadTS,
		CreatedAt: draft.CreatedAt,
		UpdatedAt: draft.UpdatedAt,
	}, nil
}

// Get retrieves a draft by ID
func (s *LocalStore) Get(id string) (*output.Draft, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	d, ok := s.drafts[id]
	if !ok {
		return nil, fmt.Errorf("draft not found: %s", id)
	}

	return &output.Draft{
		ID:        d.ID,
		ChannelID: d.ChannelID,
		Text:      d.Text,
		ThreadTS:  d.ThreadTS,
		CreatedAt: d.CreatedAt,
		UpdatedAt: d.UpdatedAt,
	}, nil
}

// Edit updates a draft's text
func (s *LocalStore) Edit(id, text string) (output.Draft, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	d, ok := s.drafts[id]
	if !ok {
		return output.Draft{}, fmt.Errorf("draft not found: %s", id)
	}

	d.Text = text
	d.UpdatedAt = time.Now().Format(time.RFC3339)

	if err := s.save(); err != nil {
		return output.Draft{}, fmt.Errorf("save draft: %w", err)
	}

	return output.Draft{
		ID:        d.ID,
		ChannelID: d.ChannelID,
		Text:      d.Text,
		ThreadTS:  d.ThreadTS,
		CreatedAt: d.CreatedAt,
		UpdatedAt: d.UpdatedAt,
	}, nil
}

// Delete removes a draft
func (s *LocalStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.drafts[id]; !ok {
		return fmt.Errorf("draft not found: %s", id)
	}

	delete(s.drafts, id)

	return s.save()
}
