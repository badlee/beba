package binder

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"http-server/plugins/config"
	"io"
	"log"
	"net"
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"
	"time"

	// autoload js modules
	_ "http-server/modules/console"
	_ "http-server/modules/cookies"
	_ "http-server/modules/dtp"
	_ "http-server/modules/fetch"
	_ "http-server/modules/fs"
	_ "http-server/modules/path"
	_ "http-server/modules/storage"

	"http-server/modules/security"
)

// Directive interface for matching and handling connections
type Directive interface {
	Name() string
	Address() string
	Start() (listener []net.Listener, err error)
	Match(peek []byte) (bool, error)
	Handle(conn net.Conn) error
	HandlePacket(data []byte, addr net.Addr, pc net.PacketConn) error
	Close() error
}

type Manager struct {
	listeners    map[string]net.Listener
	udpListeners map[string]net.PacketConn
	protocols    map[string]func(config *DirectiveConfig) (Directive, error)
	directives   []Directive
	mu           sync.RWMutex
	done         chan struct{}
	config       *config.AppConfig
}

func NewManager(cfg *config.AppConfig) *Manager {
	m := &Manager{
		protocols:    make(map[string]func(*DirectiveConfig) (Directive, error)),
		config:       cfg,
		listeners:    make(map[string]net.Listener),
		udpListeners: make(map[string]net.PacketConn),
	}

	m.RegisterDirective("HTTP", func(cfg *DirectiveConfig) (Directive, error) {
		cfg.AppConfig = m.config
		return NewHTTPDirective(cfg), nil
	})

	m.RegisterDirective("DTP", func(cfg *DirectiveConfig) (Directive, error) {
		cfg.AppConfig = m.config
		return NewDTPDirective(cfg)
	})

	m.RegisterDirective("DATABASE", func(cfg *DirectiveConfig) (Directive, error) {
		cfg.AppConfig = m.config
		return NewDatabaseDirective(cfg)
	})

	m.RegisterDirective("MAIL", func(cfg *DirectiveConfig) (Directive, error) {
		cfg.AppConfig = m.config
		return NewMailDirective(cfg)
	})
	m.RegisterDirective("SECURITY", func(cfg *DirectiveConfig) (Directive, error) {
		cfg.AppConfig = m.config
		return NewSecurityDirective(cfg)
	})

	m.RegisterDirective("MQTT", func(cfg *DirectiveConfig) (Directive, error) {
		cfg.AppConfig = m.config
		return NewMQTTDirective(cfg)
	})
	return m
}

func (m *Manager) IsKnownProtocol(name string) (ok bool) {
	return IsKnownProtocol(name)
}

func (m *Manager) GetProtocols() (protocols []string) {
	for k := range m.protocols {
		protocols = append(protocols, strings.ToUpper(k))
	}
	return
}

// GetAddresses returns all active listener addresses.
func (m *Manager) GetAddresses() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var addrs []string
	for addr := range m.listeners {
		addrs = append(addrs, addr)
	}
	return addrs
}

// GetListeners returns all active net.Listeners.
func (m *Manager) GetListeners() []net.Listener {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var lns []net.Listener
	for _, ln := range m.listeners {
		lns = append(lns, ln)
	}
	return lns
}

func (m *Manager) RegisterDirective(name string, factory func(*DirectiveConfig) (Directive, error)) {
	m.protocols[name] = factory
	RegisterProtocolKeyword(name)
}

// Start starts all listeners defined in the config. By default it blocks.
func (m *Manager) Restart() error {
	log.Printf("Manager: Restarting...")
	for _, d := range m.directives {
		go d.Close()
	}
	time.Sleep(1 * time.Second)
	exe, err := os.Executable()
	if err != nil {
		return err
	}

	return syscall.Exec(exe, os.Args, os.Environ())
}
func (m *Manager) Start(config *Config) error {
	for _, group := range config.Groups {
		// config.Protocols → config.Registrations (filtered to kind "PROTOCOL")
		if err := m.startGroup(group, config.Registrations); err != nil {
			return fmt.Errorf("%s %v: %w", group.Directive, group.Address, err)
		}
	}
	m.done = make(chan struct{})
	<-m.done
	return nil
}

func (m *Manager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	// 1. Close all directives (order is important if dependencies exist, but here we go parallel/bulk)
	for _, d := range m.directives {
		if err := d.Close(); err != nil {
			log.Printf("Manager: Error closing directive %s: %v", d.Name(), err)
		}
	}
	m.directives = nil

	// 2. Close network listeners
	for _, ln := range m.listeners {
		ln.Close()
	}
	for _, pc := range m.udpListeners {
		pc.Close()
	}
	m.listeners = make(map[string]net.Listener)
	m.udpListeners = make(map[string]net.PacketConn)
	
	if m.done != nil {
		close(m.done)
		m.done = nil
	}
	return nil
}

func (m *Manager) startGroup(group GroupConfig, registered []ProtocolRegistration) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic in startGroup: %v", r)
			debug.PrintStack()
		}
	}()
	var protocols []Directive
	for _, item := range group.Items {
		// Check for a JS-backed registered protocol first.
		// Only PROTOCOL registrations are relevant here.
		for _, reg := range registered {
			if reg.Kind != "PROTOCOL" {
				continue
			}
			if strings.EqualFold(reg.Name, item.Name) {
				item.AppConfig = m.config
				p, err := NewJSDirective(item, reg)
				if err != nil {
					return fmt.Errorf("failed to init JS protocol %s: %v", item.Name, err)
				}
				protocols = append(protocols, p)
				continue
			}
		}

		// Fallback: native protocol factory.
		factory, ok := m.protocols[item.Name]
		if !ok {
			log.Printf("Warning: Directive %s not registered", item.Name)
			continue
		}
		p, err := factory(item)
		if err != nil {
			return fmt.Errorf("failed to init protocol %s: %v", item.Name, err)
		}
		protocols = append(protocols, p)
	}

	if len(protocols) == 0 {
		return fmt.Errorf("no valid protocols for group %s %s", group.Directive, group.Address)
	}

	// Start all protocols.
	if strings.ToUpper(group.Directive) == "UDP" {
		pc, err := net.ListenPacket("udp", group.Address)
		if err != nil {
			return fmt.Errorf("failed to listen on UDP %s: %v", group.Address, err)
		}
		m.mu.Lock()
		m.udpListeners[group.Address] = pc
		for _, p := range protocols {
			m.directives = append(m.directives, p)
		}
		m.mu.Unlock()

		log.Printf("Listening on %s [UDP]", pc.LocalAddr().String())
		
		// Find the first SECURITY policy defined in any of the protocols for this port
		policyName := "default"
		for _, p := range protocols {
			if dp, ok := p.(*DTPDirective); ok {
				if sec := dp.cfg.GetRoutes("SECURITY"); len(sec) > 0 {
					policyName = sec[0].Path
					break
				}
			}
		}

		go m.udpAcceptLoop(pc, protocols, policyName)
		return nil
	}

	var address []net.Listener
	for _, p := range protocols {
		m.mu.Lock()
		m.directives = append(m.directives, p)
		m.mu.Unlock()

		log.Printf("Manager: Calling Start() on protocol %s...", p.Name())
		a, err := p.Start()
		if err != nil {
			return fmt.Errorf("failed to start protocol %s: %v", p.Name(), err)
		}
		log.Printf("Manager: Protocol %s started, got %d listeners", p.Name(), len(a))
		address = append(address, a...)
	}

	if len(address) == 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.listeners == nil {
		m.listeners = make(map[string]net.Listener)
	}
	for _, ln := range address {
		m.listeners[ln.Addr().String()] = ln
		log.Printf("Listening on %s [%s]", ln.Addr().String(), group.Directive)

		// Find the first SECURITY policy defined in any of the protocols for this port
		policyName := "default"
		for _, p := range protocols {
			if dp, ok := p.(*MQTTDirective); ok {
				if sec := dp.cfg.GetRoutes("SECURITY"); len(sec) > 0 {
					policyName = sec[0].Path
					break
				}
			}
			// Add other protocol types here if they support SECURITY (e.g. HTTPDirective)
			if hd, ok := p.(*HTTPDirective); ok {
				if sec := hd.cfg.GetRoutes("SECURITY"); len(sec) > 0 {
					policyName = sec[0].Path
					break
				}
			}
		}

		go m.acceptLoop(ln, protocols, policyName)
	}
	return nil
}

func (m *Manager) udpAcceptLoop(pc net.PacketConn, protocols []Directive, policyName string) {
	buf := make([]byte, 2048)
	for {
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			log.Printf("UDP ReadFrom error: %v", err)
			continue
		}

		packetData := make([]byte, n)
		copy(packetData, buf[:n])

		// Check against Global/Default OR Protocol-specific Security Rules
		if !security.GetEngine().AllowPacket(addr, policyName) {
			log.Printf("Manager: UDP Packet from %s BLOCKED by policy %q", addr, policyName)
			continue
		}

		// Dispatch to protocols
		go func(data []byte, remoteAddr net.Addr) {
			for _, p := range protocols {
				matched, err := p.Match(data)
				if err != nil {
					log.Printf("Error matching protocol %s: %v", p.Name(), err)
					continue
				}
				if matched {
					if err := p.HandlePacket(data, remoteAddr, pc); err != nil {
						log.Printf("Directive %s HandlePacket error: %v", p.Name(), err)
					}
					return
				}
			}
			log.Printf("Manager: No protocol matched for UDP packet from %s", remoteAddr)
		}(packetData, addr)
	}
}

func (m *Manager) acceptLoop(ln net.Listener, protocols []Directive, policyName string) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("Accept error: %v", err)
			continue
		}

		// Check against Global/Default OR Protocol-specific Security Rules
		log.Printf("Manager: Accepted connection from %s, checking security policy %q", conn.RemoteAddr(), policyName)
		if !security.GetEngine().AllowConnection(conn, policyName) {
			log.Printf("Manager: Connection from %s BLOCKED by policy %q", conn.RemoteAddr(), policyName)
			conn.Close()
			continue
		}

		log.Printf("Manager: Connection from %s ALLOWED, dispatching handler", conn.RemoteAddr())
		go m.handleConnection(conn, protocols)
	}
}

func (m *Manager) handleConnection(conn net.Conn, protocols []Directive) {
	br := bufio.NewReader(conn)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	log.Printf("Manager: Peeking bytes from %s...", conn.RemoteAddr())

	peekData, err := br.Peek(512)
	// If it's EOF or error, we still try to match whatever we got unless it's a real timeout error
	if err != nil && err != io.EOF && !errors.Is(err, bufio.ErrBufferFull) {
		// Ignore timeout if we got some data
		if len(peekData) == 0 {
			log.Printf("Manager: Peek read error from %s: %v", conn.RemoteAddr(), err)
			conn.Close()
			return
		}
	}
	conn.SetReadDeadline(time.Time{})
	log.Printf("Manager: Peeked %d bytes from %s", len(peekData), conn.RemoteAddr())

	wrappedConn := &PeekedConn{
		Conn: conn,
		r:    br,
	}

	for _, p := range protocols {
		matched, err := p.Match(peekData)
		if err != nil {
			log.Printf("Error matching protocol %s: %v", p.Name(), err)
			continue
		}
		if matched {
			log.Printf("Manager: Protocol matched: %s for %s", p.Name(), conn.RemoteAddr())
			if err := p.Handle(wrappedConn); err != nil {
				log.Printf("Directive %s handle error: %v", p.Name(), err)
			}
			return
		}
	}
	log.Printf("Manager: No protocol matched for %s, closing connection.", conn.RemoteAddr())
	wrappedConn.Close()
}

// Validate checks if the peeked bytes match any registered directive on a specific address/port.
// proto is the protocol of the proxy (tcp, udp).
func (m *Manager) Validate(proto, port string, data []byte) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, d := range m.directives {
		// Filter by address if the directive has one
		addr := d.Address()
		if addr != "" {
			_, dPort, err := net.SplitHostPort(addr)
			if err == nil && dPort != port {
				continue
			}
		}

		if match, _ := d.Match(data); match {
			return true
		}
	}
	return false
}

func (m *Manager) HandleWithPeek(conn net.Conn, peek []byte) {
	wrapped := &PeekedConn{
		Conn: conn,
		r:    io.MultiReader(bytes.NewReader(peek), conn),
	}
	m.mu.RLock()
	protocols := make([]Directive, len(m.directives))
	copy(protocols, m.directives)
	m.mu.RUnlock()

	go m.handleConnection(wrapped, protocols)
}

type PeekedConn struct {
	net.Conn
	r io.Reader
}

func (c *PeekedConn) Read(p []byte) (n int, err error) {
	return c.r.Read(p)
}
