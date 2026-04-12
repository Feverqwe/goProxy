package handler

import (
	"net"
	"sync"
	"time"
)

type UDPSession struct {
	RemoteConn net.Conn
	LastActive time.Time
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
	s.LastActive = time.Now()
	return s.RemoteConn, true
}

func (m *UDPSessionManager) Set(clientAddr string, conn net.Conn) {
	m.sessions.Store(clientAddr, &UDPSession{
		RemoteConn: conn,
		LastActive: time.Now(),
	})
}

func (m *UDPSessionManager) cleanup() {
	ticker := time.NewTicker(m.ttl / 2)
	for range ticker.C {
		m.sessions.Range(func(key, value interface{}) bool {
			session := value.(*UDPSession)
			if time.Since(session.LastActive) > m.ttl {
				session.RemoteConn.Close()
				m.sessions.Delete(key)
			}
			return true
		})
	}
}
