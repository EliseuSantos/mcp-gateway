package proxy

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
)

// Session is the downstream session the client holds.
// UpstreamID is the id the gateway presents to the upstream. It is fixed
// when the session is published and is not a client-visible value.
type Session struct {
	id         string
	upstreamID string
}

// ID returns the downstream session id.
func (s *Session) ID() string {
	if s == nil {
		return ""
	}
	return s.id
}

// UpstreamID returns the upstream session id, or empty when the upstream
// did not assign one.
func (s *Session) UpstreamID() string {
	if s == nil {
		return ""
	}
	return s.upstreamID
}

type sessionStore struct {
	mu sync.Mutex
	m  map[string]*Session
}

func newSessionStore() *sessionStore {
	return &sessionStore{m: make(map[string]*Session)}
}

func (s *sessionStore) create(upstreamID string) (*Session, error) {
	id, err := newSessionID()
	if err != nil {
		return nil, err
	}
	sess := &Session{id: id, upstreamID: upstreamID}
	s.mu.Lock()
	s.m[id] = sess
	s.mu.Unlock()
	return sess, nil
}

func (s *sessionStore) get(id string) (*Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.m[id]
	return sess, ok
}

func (s *sessionStore) delete(id string) (*Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.m[id]
	if ok {
		delete(s.m, id)
	}
	return sess, ok
}

func (s *sessionStore) list() []*Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Session, 0, len(s.m))
	for _, sess := range s.m {
		out = append(out, sess)
	}
	return out
}

func newSessionID() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}
