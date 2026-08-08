package handler

import (
	"net"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

type UDPSession struct {
	RemoteConn net.Conn
	mu         sync.Mutex
	lastActive time.Time
	closed     bool
	listenOnce sync.Once
}

type UDPSessionManager struct {
	sessions sync.Map // map[string]*UDPSession
	ttl      time.Duration
	create   singleflight.Group
}

func NewUDPSessionManager(ttl time.Duration) *UDPSessionManager {
	m := &UDPSessionManager{
		ttl: ttl,
	}

	go m.cleanup()
	return m
}

func (m *UDPSessionManager) Get(clientAddr string) (*UDPSession, bool) {
	for {
		val, ok := m.sessions.Load(clientAddr)
		if !ok {
			return nil, false
		}
		session := val.(*UDPSession)
		if session.touch() {
			return session, true
		}
		m.sessions.CompareAndDelete(clientAddr, session)
	}
}

func (m *UDPSessionManager) LoadOrStore(clientAddr string, conn net.Conn) (*UDPSession, bool) {
	candidate := newUDPSession(conn)
	for {
		value, loaded := m.sessions.LoadOrStore(clientAddr, candidate)
		if !loaded {
			return candidate, false
		}

		existing := value.(*UDPSession)
		if existing.touch() {
			candidate.close()
			return existing, true
		}
		m.sessions.CompareAndDelete(clientAddr, existing)
	}
}

func (m *UDPSessionManager) GetOrCreate(clientAddr string, createConn func() (net.Conn, error)) (*UDPSession, error) {
	if session, ok := m.Get(clientAddr); ok {
		return session, nil
	}

	value, err, _ := m.create.Do(clientAddr, func() (interface{}, error) {
		if session, ok := m.Get(clientAddr); ok {
			return session, nil
		}

		conn, err := createConn()
		if err != nil {
			return nil, err
		}
		session, _ := m.LoadOrStore(clientAddr, conn)
		return session, nil
	})
	if err != nil {
		return nil, err
	}

	session := value.(*UDPSession)
	if !session.touch() {
		m.sessions.CompareAndDelete(clientAddr, session)
		return m.GetOrCreate(clientAddr, createConn)
	}
	return session, nil
}

func newUDPSession(conn net.Conn) *UDPSession {
	return &UDPSession{
		RemoteConn: conn,
		lastActive: time.Now(),
	}
}

func (s *UDPSession) touch() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.lastActive = time.Now()
	return true
}

func (s *UDPSession) conn() (net.Conn, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, false
	}
	return s.RemoteConn, true
}

func (s *UDPSession) startListener(start func()) {
	s.listenOnce.Do(start)
}

func (s *UDPSession) close() bool {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return false
	}
	s.closed = true
	conn := s.RemoteConn
	s.mu.Unlock()
	conn.Close()
	return true
}

func (s *UDPSession) closeIfExpired(now time.Time, ttl time.Duration) bool {
	s.mu.Lock()
	if s.closed || now.Sub(s.lastActive) <= ttl {
		s.mu.Unlock()
		return false
	}
	s.closed = true
	conn := s.RemoteConn
	s.mu.Unlock()
	conn.Close()
	return true
}

func (m *UDPSessionManager) Delete(clientAddr string, session *UDPSession) {
	m.sessions.CompareAndDelete(clientAddr, session)
	session.close()
}

func (m *UDPSessionManager) cleanup() {
	ticker := time.NewTicker(m.ttl / 2)
	for range ticker.C {
		now := time.Now()
		m.sessions.Range(func(key, value interface{}) bool {
			session := value.(*UDPSession)
			if session.closeIfExpired(now, m.ttl) {
				m.sessions.CompareAndDelete(key, session)
			}
			return true
		})
	}
}
