//go:build windows

package main

import (
	"net"

	"github.com/Microsoft/go-winio"
)

func listenSocket(network, address string) (net.Listener, error) {
	if network == "npipe" {
		return winio.ListenPipe(address, nil)
	}
	return net.Listen(network, address)
}

func dialSocket(network, address string) (net.Conn, error) {
	if network == "npipe" {
		return winio.DialPipe(address, nil)
	}
	return net.Dial(network, address)
}
