//go:build !windows

package main

import (
	"net"
)

func listenSocket(network, address string) (net.Listener, error) {
	return net.Listen(network, address)
}

func dialSocket(network, address string) (net.Conn, error) {
	return net.Dial(network, address)
}
