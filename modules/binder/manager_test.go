package binder

import (
	"http-server/plugins/config"
	"net"
	"testing"
)

func TestManagerLifecycle(t *testing.T) {
	cfg := &config.AppConfig{}
	m := NewManager(cfg)

	if m == nil {
		t.Fatal("NewManager returned nil")
	}

	// Register a mock protocol
	mockDirective := &mockDirective{name: "MOCK", addr: "127.0.0.1:0"}
	m.RegisterDirective("MOCK", func(cfg *DirectiveConfig) (Directive, error) {
		return mockDirective, nil
	})

	// Start with a mock configuration
	mgrCfg := &Config{
		Groups: []GroupConfig{
			{
				Directive: "MOCK",
				Address:   "127.0.0.1:0",
				Items: []*DirectiveConfig{
					{Name: "MOCK"},
				},
			},
		},
	}

	// Since Manager.Start blocks, we run it in a goroutine or use a mock.
	// Actually, Start blocks on <-m.done.
	go func() {
		_ = m.Start(mgrCfg)
	}()

	// Wait for start
	for i := 0; i < 10; i++ {
		m.mu.RLock()
		started := len(m.listeners) > 0
		m.mu.RUnlock()
		if started {
			break
		}
		// wait a bit
	}

	m.Stop()
}

type mockDirective struct {
	name    string
	addr    string
	ln      []net.Listener
	handled bool
}

func (m *mockDirective) Name() string    { return m.name }
func (m *mockDirective) Address() string { return m.addr }
func (m *mockDirective) Start() ([]net.Listener, error) {
	ln, err := net.Listen("tcp", m.addr)
	if err != nil {
		return nil, err
	}
	m.ln = append(m.ln, ln)
	return m.ln, nil
}
func (m *mockDirective) Match(peek []byte) (bool, error) { return true, nil }
func (m *mockDirective) Handle(conn net.Conn) error {
	m.handled = true
	return nil
}

func (m *mockDirective) HandlePacket(data []byte, addr net.Addr, pc net.PacketConn) error {
	return nil
}

func (m *mockDirective) Close() error {
	for _, l := range m.ln {
		l.Close()
	}
	return nil
}
