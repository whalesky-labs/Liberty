package media

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
)

var ErrSelection = errors.New("media_selection_invalid")

type grant struct {
	path string
	info os.FileInfo
}

// Selections accepts paths only from the native file dialog adapter. Views and
// use cases receive opaque tokens; a filename is never itself a file grant.
type Selections struct {
	mu    sync.Mutex
	items map[string]grant
}

func NewSelections() *Selections { return &Selections{items: map[string]grant{}} }
func (s *Selections) Authorize(path string) (application.SelectedMedia, error) {
	if !filepath.IsAbs(path) {
		return application.SelectedMedia{}, ErrSelection
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return application.SelectedMedia{}, ErrSelection
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.Mode().IsRegular() {
		return application.SelectedMedia{}, ErrSelection
	}
	kind := "audio"
	switch strings.ToLower(filepath.Ext(canonical)) {
	case ".m4a", ".mp3", ".wav", ".aac", ".flac":
	case ".mp4", ".mov", ".mkv":
		kind = "video"
	default:
		return application.SelectedMedia{}, ErrSelection
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return application.SelectedMedia{}, ErrSelection
	}
	token := hex.EncodeToString(nonce[:])
	s.mu.Lock()
	defer s.mu.Unlock()
	// A replacement dialog may grant 32 new files before the controller releases
	// the previous 32. The bound does not increase concurrent ASR workers.
	if len(s.items) >= 64 {
		return application.SelectedMedia{}, ErrSelection
	}
	s.items[token] = grant{canonical, info}
	return application.SelectedMedia{Token: token, Name: filepath.Base(canonical), Kind: kind}, nil
}
func (s *Selections) Resolve(ctx context.Context, token string) (application.MediaInput, error) {
	if err := ctx.Err(); err != nil {
		return application.MediaInput{}, err
	}
	s.mu.Lock()
	g, ok := s.items[token]
	s.mu.Unlock()
	if !ok {
		return application.MediaInput{}, ErrSelection
	}
	info, err := os.Lstat(g.path)
	if err != nil || !info.Mode().IsRegular() || !os.SameFile(g.info, info) {
		return application.MediaInput{}, ErrSelection
	}
	kind := "audio"
	switch strings.ToLower(filepath.Ext(g.path)) {
	case ".mp4", ".mov", ".mkv":
		kind = "video"
	}
	return application.MediaInput{Path: g.path, Source: domain.SourceFile{Name: filepath.Base(g.path), Kind: kind}}, nil
}
func (s *Selections) Release(token string) { s.mu.Lock(); delete(s.items, token); s.mu.Unlock() }
