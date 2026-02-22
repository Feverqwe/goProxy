//go:build !linux

package handler

import (
	"context"
	"net"
)

func BindToInterface(_ uintptr, _ string) error {
	return nil
}

func NewBoundUDPConn(ctx context.Context, ifName string) (net.PacketConn, error) {
	return net.ListenPacket("udp", ":0")
}
