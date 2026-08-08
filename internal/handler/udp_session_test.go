package handler

import (
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestUDPSessionActivityIsConcurrentSafe(t *testing.T) {
	conn, peer := net.Pipe()
	defer peer.Close()
	session := newUDPSession(conn)

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 1_000 {
				if !session.touch() {
					return
				}
				_, _ = session.conn()
			}
		}()
	}
	wg.Wait()

	if session.closeIfExpired(time.Now(), time.Minute) {
		t.Fatal("freshly touched session was closed as expired")
	}
	session.mu.Lock()
	session.lastActive = time.Now().Add(-2 * time.Minute)
	session.mu.Unlock()
	if !session.closeIfExpired(time.Now(), time.Minute) {
		t.Fatal("old session was not closed")
	}
	if session.touch() {
		t.Fatal("closed session became active again")
	}
	if _, ok := session.conn(); ok {
		t.Fatal("closed session exposed its connection")
	}
}

func TestUDPSessionManagerGetOrCreateIsAtomic(t *testing.T) {
	manager := &UDPSessionManager{ttl: time.Minute}

	const workers = 16
	results := make(chan *UDPSession, workers)
	start := make(chan struct{})
	var createCount atomic.Int32
	var peersMu sync.Mutex
	var peers []net.Conn
	createConn := func() (net.Conn, error) {
		createCount.Add(1)
		conn, peer := net.Pipe()
		peersMu.Lock()
		peers = append(peers, peer)
		peersMu.Unlock()
		return conn, nil
	}

	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			session, err := manager.GetOrCreate("client->target", createConn)
			if err != nil {
				t.Errorf("GetOrCreate: %v", err)
				return
			}
			results <- session
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	defer func() {
		for _, peer := range peers {
			peer.Close()
		}
	}()

	if got := createCount.Load(); got != 1 {
		t.Fatalf("connection creations = %d, want 1", got)
	}
	var winner *UDPSession
	for session := range results {
		if winner == nil {
			winner = session
		}
		if session != winner {
			t.Fatal("concurrent GetOrCreate returned different sessions")
		}
	}
	current, ok := manager.Get("client->target")
	if !ok || current != winner {
		t.Fatal("manager did not retain the single winning session")
	}
	manager.Delete("client->target", winner)
	if _, ok := manager.Get("client->target"); ok {
		t.Fatal("deleted session is still available")
	}
}

func TestUDPSessionManagerStaleDeleteKeepsReplacement(t *testing.T) {
	manager := &UDPSessionManager{ttl: time.Minute}

	oldConn, oldPeer := net.Pipe()
	defer oldPeer.Close()
	oldSession, loaded := manager.LoadOrStore("client->target", oldConn)
	if loaded {
		t.Fatal("first session unexpectedly reused an existing session")
	}
	manager.Delete("client->target", oldSession)

	newConn, newPeer := net.Pipe()
	defer newPeer.Close()
	replacement, loaded := manager.LoadOrStore("client->target", newConn)
	if loaded {
		t.Fatal("replacement unexpectedly reused an existing session")
	}

	manager.Delete("client->target", oldSession)
	current, ok := manager.Get("client->target")
	if !ok || current != replacement {
		t.Fatal("deleting an old session removed its replacement")
	}
	manager.Delete("client->target", replacement)
}

func TestUDPSessionListenerStartsOnce(t *testing.T) {
	conn, peer := net.Pipe()
	defer peer.Close()
	session := newUDPSession(conn)
	defer session.close()

	var starts atomic.Int32
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			session.startListener(func() {
				starts.Add(1)
			})
		}()
	}
	wg.Wait()

	if got := starts.Load(); got != 1 {
		t.Fatalf("listener starts = %d, want 1", got)
	}
}
