package handler

import (
	"net"
	"sync"
	"sync/atomic"
	"time"
)

type UDPSession struct {
	RemoteConn net.Conn
	lastActive atomic.Int64
}

type UDPSessionManager struct {
	sessions sync.Map // map[string]*UDPSession
	ttl      time.Duration
}

func NewUDPSessionManager(ttl time.Duration) *UDPSessionManager {
	m := &UDPSessionManager{
		ttl: ttl,
	}

	go m.cleanup()
	return m
}

func (m *UDPSessionManager) Get(clientAddr string) (net.Conn, bool) {
	val, ok := m.sessions.Load(clientAddr)
	if !ok {
		return nil, false
	}
	s := val.(*UDPSession)
	s.touch()
	return s.RemoteConn, true
}

func (m *UDPSessionManager) Set(clientAddr string, conn net.Conn) {
	session := &UDPSession{RemoteConn: conn}
	session.touch()
	m.sessions.Store(clientAddr, session)
}

func (s *UDPSession) touch() {
	s.lastActive.Store(time.Now().UnixNano())
}

func (s *UDPSession) expired(now time.Time, ttl time.Duration) bool {
	return now.Sub(time.Unix(0, s.lastActive.Load())) > ttl
}

func (m *UDPSessionManager) cleanup() {
	ticker := time.NewTicker(m.ttl / 2)
	for range ticker.C {
		now := time.Now()
		m.sessions.Range(func(key, value interface{}) bool {
			session := value.(*UDPSession)
			if session.expired(now, m.ttl) {
				session.RemoteConn.Close()
				m.sessions.Delete(key)
			}
			return true
		})
	}
}
