//go:build linux

package handler

import (
	"context"
	"net"
	"syscall"

	"golang.org/x/sys/unix"
)

func BindToInterface(fd uintptr, ifName string) error {
	return unix.SetsockoptString(int(fd), unix.SOL_SOCKET, unix.SO_BINDTODEVICE, ifName)
}

func NewBoundUDPConn(ctx context.Context, ifName string) (net.PacketConn, error) {
	lc := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			return c.Control(func(fd uintptr) {
				BindToInterface(fd, ifName)
			})
		},
	}
	return lc.ListenPacket(ctx, "udp", ":0")
}
