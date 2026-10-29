package porttest

import (
	"context"
	"io"
	"maps"
	"slices"
	"sync"

	"github.com/dihemoe/alisten/internal/app/port"
)

var _ port.MediaStore = (*MediaStore)(nil)

type MediaStore struct {
	mu      sync.Mutex
	objects map[string]Object
	failure error
}

type Object struct {
	Data        []byte
	ContentType string
}

func NewMediaStore() *MediaStore {
	return &MediaStore{objects: map[string]Object{}}
}

func (s *MediaStore) Put(_ context.Context, key string, body io.ReadSeeker, contentType string) error {
	data, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failure != nil {
		return s.failure
	}
	s.objects[key] = Object{Data: data, ContentType: contentType}
	return nil
}

func (s *MediaStore) Exists(_ context.Context, key string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failure != nil {
		return false, s.failure
	}
	_, ok := s.objects[key]
	return ok, nil
}

func (s *MediaStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failure != nil {
		return s.failure
	}
	delete(s.objects, key)
	return nil
}

func (s *MediaStore) Object(key string) (o Object, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok = s.objects[key]
	return o, ok
}

func (s *MediaStore) Keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Sorted(maps.Keys(s.objects))
}

func (s *MediaStore) Fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failure = err
}
