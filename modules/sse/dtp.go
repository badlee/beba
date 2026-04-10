package sse

import (
	"log"
	"net"
	"time"

	"github.com/limba/dtp/pkg/dtp"
	"github.com/limba/dtp/pkg/dtpserver"
)

// DTPServer is a wrapper around the DTP protocol server that integrates with the Hub.
type DTPServer struct {
	server *dtpserver.Server
}

// NewDTPServer creates a new DTP server instance.
func NewDTPServer(timeout time.Duration) *DTPServer {
	srv := dtpserver.NewServer("0.0.0.0:0", timeout)
	
	d := &DTPServer{
		server: srv,
	}

	// Default handlers to bridge DTP to Hub
	srv.On(dtp.TypeData, d.bridgeToHub)
	srv.On(dtp.TypeEvent, d.bridgeToHub)
	srv.On(dtp.TypeCmd, d.bridgeToHub)
	srv.On(dtp.TypePing, d.bridgeToHub)
	srv.On(dtp.TypePong, d.bridgeToHub)
	srv.On(dtp.TypeACK, d.bridgeToHub)
	srv.On(dtp.TypeNACK, d.bridgeToHub)
	srv.On(dtp.TypeError, d.bridgeToHub)

	return d
}

// HandleConnection injects a TCP connection into the DTP server.
func (s *DTPServer) HandleConnection(conn net.Conn) {
	s.server.HandleConnection(conn)
}

// HandlePacket injects a UDP packet into the DTP server.
func (s *DTPServer) HandlePacket(conn *net.UDPConn, addr *net.UDPAddr, data []byte) {
	s.server.HandleUDPPacket(conn, addr, data)
}

// bridgeToHub captures DTP packets and publishes them to the SSE Hub.
func (s *DTPServer) bridgeToHub(device *dtpserver.DeviceConfig, pkt *dtp.Packet) {
	if device == nil {
		return
	}

	channel := "dtp.device." + device.DeviceID.String()
	event := pkt.Type.String()
	if pkt.Subtype != 0 {
		event += "." + pkt.Type.SubtypeToString(pkt.Subtype)
	}

	log.Printf("DTP: Bridging packet to Hub: channel=%s event=%s", channel, event)

	HubInstance.Publish(&Message{
		Channel: channel,
		Event:   event,
		Data:    string(pkt.Payload),
		Source:  "dtp",
	})

	// Also publish to a global DTP channel
	HubInstance.Publish(&Message{
		Channel: "dtp.all",
		Event:   event,
		Data:    string(pkt.Payload),
		Source:  "dtp",
	})
}

// Server returns the underlying DTP server instance.
func (s *DTPServer) Server() *dtpserver.Server {
	return s.server
}
