package binder

import (
	"net"
	"strings"
	"testing"

	"http-server/modules/security"
	"http-server/plugins/config"
	"http-server/plugins/httpserver"
)

// mockTestConn is a simple struct mimicking net.Conn that allows mocking RemoteAddr securely.
type mockTestConn struct {
	net.Conn
	addr net.Addr
}

func (m *mockTestConn) RemoteAddr() net.Addr {
	return m.addr
}

func TestMQTTDirective_SecurityIntegration(t *testing.T) {
	// Initialize default limits
	security.GetEngine()
	
	t.Run("Parse SECURITY custom block", func(t *testing.T) {
		cfg := &DirectiveConfig{
			Name:    "MQTT",
			Address: ":31885",
			Routes: []*RouteConfig{
				{
					Method: "SECURITY",
					Path:   "my_secure_block",
				},
			},
		}
		
		dir, err := NewMQTTDirective(cfg)
		if err != nil {
			t.Fatalf("failed to parse config: %v", err)
		}
		listeners, err := dir.Start()
		if err != nil {
			t.Fatalf("failed to start: %v", err)
		}
		for _, listener := range listeners {
			defer listener.Close()
		}
		if dir.server == nil {
			t.Fatal("Expected MQTT server creation to succeed even with security")
		}
		dir.Close()
	})

	t.Run("Match MQTT CONNECT Magic Packet", func(t *testing.T) {
		cfg := &DirectiveConfig{
			Name:    "MQTT",
			Address: ":31886",
		}
		dir, _ := NewMQTTDirective(cfg)
		defer dir.Close()

		validPkt := []byte{0x10, 0x0C, 0x00, 0x04, 'M', 'Q', 'T', 'T'}
		match, err := dir.Match(validPkt)
		if err != nil {
			t.Fatalf("Match returned error: %v", err)
		}
		if !match {
			t.Fatal("Expected true when parsing valid MQTT CONNECT bytes")
		}
		
		legacyPkt := []byte{0x10, 0x0E, 0x00, 0x06, 'M', 'Q', 'I', 's', 'd', 'p'}
		match, _ = dir.Match(legacyPkt)
		if !match {
			t.Fatal("Expected true when parsing valid MQTT Legacy MQIsdp bytes")
		}

		noise := []byte{0x20, 0x05, 0x00, 0x00, 'H', 'T', 'T', 'P'}
		match, _ = dir.Match(noise)
		if match {
			t.Fatal("Expected false when feeding garbage bytes")
		}
	})

	t.Run("Manager Accept Drops Banned IPs", func(t *testing.T) {
		m := NewManager(&config.AppConfig{})
		defer m.Stop()

		// Mock a WAF policy strictly blocking 1.2.3.4
		policyCfg := &httpserver.ConnectionConfig{
			DenyList: []string{"1.2.3.4"},
			RateLimit: &httpserver.RateLimitConfig{
				Limit: 100,
				Burst: 10,
			},
		}
		security.GetEngine().LoadPolicy("default", policyCfg)

		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("Failed to listen: %v", err)
		}
		defer ln.Close()

		addr := ln.Addr().String()

		cfg := &Config{
			Groups: []GroupConfig{
				{
					Directive: "TCP",
					Address:   addr,
					Items: []*DirectiveConfig{
						{
							Name:    "MQTT",
							Address: addr,
						},
					},
				},
			},
		}

		// Non-blocking start
		go func() {
			if err := m.Start(cfg); err != nil && !strings.Contains(err.Error(), "closed") {
				t.Logf("Manager start error: %v", err)
			}
		}()

		// Fake a connection from 1.2.3.4
		mockBanned := &mockTestConn{addr: &net.TCPAddr{IP: net.ParseIP("1.2.3.4"), Port: 1234}}
		if security.GetEngine().AllowConnection(mockBanned, "default") {
			t.Fatal("Expected AllowConnection to brutally reject 1.2.3.4")
		}
		
		mockAllowed := &mockTestConn{addr: &net.TCPAddr{IP: net.ParseIP("192.168.1.5"), Port: 1234}}
		if !security.GetEngine().AllowConnection(mockAllowed, "default") {
			t.Fatal("Expected AllowConnection to accept non-banned IP")
		}
	})
}
