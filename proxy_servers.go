package main

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"

	"github.com/txthinking/socks5"
)

type runningHTTPServer struct {
	server   *http.Server
	listener net.Listener
	close    sync.Once
}

func startHTTPProxyServer(addr string, handler http.Handler) (*runningHTTPServer, <-chan error, error) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, nil, fmt.Errorf("listen HTTP proxy on %s: %w", addr, err)
	}

	running := &runningHTTPServer{
		server:   &http.Server{Addr: addr, Handler: handler},
		listener: listener,
	}
	serveErr := make(chan error, 1)
	go func() {
		err := running.server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) || errors.Is(err, net.ErrClosed) {
			err = nil
		}
		serveErr <- err
		close(serveErr)
	}()

	return running, serveErr, nil
}

func (s *runningHTTPServer) Close() error {
	if s == nil {
		return nil
	}
	var closeErr error
	s.close.Do(func() {
		listenerErr := s.listener.Close()
		if errors.Is(listenerErr, net.ErrClosed) {
			listenerErr = nil
		}
		serverErr := s.server.Close()
		if errors.Is(serverErr, http.ErrServerClosed) || errors.Is(serverErr, net.ErrClosed) {
			serverErr = nil
		}
		closeErr = errors.Join(listenerErr, serverErr)
	})
	return closeErr
}

type runningSOCKS5Server struct {
	server      *socks5.Server
	tcpListener *net.TCPListener
	udpConn     *net.UDPConn
	close       sync.Once
}

func startSOCKS5ProxyServer(addr string, handler socks5.Handler) (*runningSOCKS5Server, <-chan error, error) {
	server, err := socks5.NewClassicServer(addr, "", "", "", 30, 30)
	if err != nil {
		return nil, nil, fmt.Errorf("initialize SOCKS5 proxy on %s: %w", addr, err)
	}
	if handler == nil {
		handler = &socks5.DefaultHandle{}
	}
	server.Handle = handler

	tcpAddr, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve SOCKS5 TCP address %s: %w", addr, err)
	}
	tcpListener, err := net.ListenTCP("tcp", tcpAddr)
	if err != nil {
		return nil, nil, fmt.Errorf("listen SOCKS5 TCP on %s: %w", addr, err)
	}

	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		_ = tcpListener.Close()
		return nil, nil, fmt.Errorf("resolve SOCKS5 UDP address %s: %w", addr, err)
	}
	udpConn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		_ = tcpListener.Close()
		return nil, nil, fmt.Errorf("listen SOCKS5 UDP on %s: %w", addr, err)
	}
	server.UDPConn = udpConn

	running := &runningSOCKS5Server{
		server:      server,
		tcpListener: tcpListener,
		udpConn:     udpConn,
	}
	rawErrors := make(chan error, 2)
	serveErr := make(chan error, 1)

	go func() {
		err := running.serveTCP()
		if err != nil && !errors.Is(err, net.ErrClosed) {
			_ = running.Close()
		} else {
			err = nil
		}
		rawErrors <- err
	}()
	go func() {
		err := running.serveUDP()
		if err != nil && !errors.Is(err, net.ErrClosed) {
			_ = running.Close()
		} else {
			err = nil
		}
		rawErrors <- err
	}()
	go func() {
		firstErr := <-rawErrors
		secondErr := <-rawErrors
		serveErr <- errors.Join(firstErr, secondErr)
		close(serveErr)
	}()

	return running, serveErr, nil
}

func (s *runningSOCKS5Server) serveTCP() error {
	for {
		conn, err := s.tcpListener.AcceptTCP()
		if err != nil {
			return err
		}
		go s.handleTCPConnection(conn)
	}
}

func (s *runningSOCKS5Server) handleTCPConnection(conn *net.TCPConn) {
	defer conn.Close()
	if err := s.server.Negotiate(conn); err != nil {
		log.Printf("SOCKS5 negotiation error: %v", err)
		return
	}
	request, err := s.server.GetRequest(conn)
	if err != nil {
		log.Printf("SOCKS5 request error: %v", err)
		return
	}
	if err := s.server.Handle.TCPHandle(s.server, conn, request); err != nil {
		log.Printf("SOCKS5 TCP handler error: %v", err)
	}
}

func (s *runningSOCKS5Server) serveUDP() error {
	for {
		buffer := make([]byte, 65507)
		n, addr, err := s.udpConn.ReadFromUDP(buffer)
		if err != nil {
			return err
		}
		packet := append([]byte(nil), buffer[:n]...)
		go s.handleUDPDatagram(addr, packet)
	}
}

func (s *runningSOCKS5Server) handleUDPDatagram(addr *net.UDPAddr, packet []byte) {
	datagram, err := socks5.NewDatagramFromBytes(packet)
	if err != nil {
		log.Printf("SOCKS5 UDP datagram error: %v", err)
		return
	}
	if datagram.Frag != 0x00 {
		log.Printf("SOCKS5 UDP fragmented datagram ignored: %d", datagram.Frag)
		return
	}
	if err := s.server.Handle.UDPHandle(s.server, addr, datagram); err != nil {
		log.Printf("SOCKS5 UDP handler error: %v", err)
	}
}

func (s *runningSOCKS5Server) Close() error {
	if s == nil {
		return nil
	}
	var closeErr error
	s.close.Do(func() {
		tcpErr := s.tcpListener.Close()
		if errors.Is(tcpErr, net.ErrClosed) {
			tcpErr = nil
		}
		udpErr := s.udpConn.Close()
		if errors.Is(udpErr, net.ErrClosed) {
			udpErr = nil
		}
		closeErr = errors.Join(tcpErr, udpErr)
	})
	return closeErr
}
