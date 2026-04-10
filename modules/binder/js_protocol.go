package binder

import (
	"fmt"
	"http-server/processor"
	"net"
	"sync"

	"github.com/dop251/goja"
)

// JSDirective adapts a Javascript script to the Directive interface
type JSDirective struct {
	name           string
	args           map[string]string
	vm             *goja.Runtime
	matchFn        goja.Callable
	handleFn       goja.Callable
	handlePacketFn goja.Callable
	startFn        goja.Callable
}

func NewJSDirective(item *DirectiveConfig, config ProtocolRegistration) (*JSDirective, error) {
	vm := processor.New(item.BaseDir, nil, item.AppConfig)
	// Execute the script
	_, err := vm.RunString(config.Code)
	if err != nil {
		return nil, fmt.Errorf("js compile error: %v", err)
	}

	processor.Register(vm, "Directives", item)
	processor.Register(vm, "Args", config.Args)

	// Get the match function
	matchVal := vm.Get("match")
	if matchVal == nil || goja.IsUndefined(matchVal) {
		return nil, fmt.Errorf("function match(buffer) not defined in script for protocol %s", item.Name)
	}
	matchFn, ok := goja.AssertFunction(matchVal)
	if !ok {
		return nil, fmt.Errorf("match is not a function in script for protocol %s", item.Name)
	}

	// Get the handle function (optional during Match phase, required for Handle)
	handleVal := vm.Get("handle")
	var handleFn goja.Callable
	if handleVal != nil && !goja.IsUndefined(handleVal) {
		handleFn, _ = goja.AssertFunction(handleVal)
	}
	
	packetVal := vm.Get("handlePacket")
	var handlePacketFn goja.Callable
	if packetVal != nil && !goja.IsUndefined(packetVal) {
		handlePacketFn, _ = goja.AssertFunction(packetVal)
	}

	// Get the handle function (optional during Match phase, required for Handle)
	startVal := vm.Get("start")
	var startFn goja.Callable
	if startVal != nil && !goja.IsUndefined(startVal) {
		startFn, _ = goja.AssertFunction(startVal)
	}

	return &JSDirective{
		name:           item.Name,
		args:           config.Args,
		vm:             vm,
		matchFn:        matchFn,
		handleFn:       handleFn,
		handlePacketFn: handlePacketFn,
		startFn:        startFn,
	}, nil
}

func (p *JSDirective) Name() string    { return p.name }
func (p *JSDirective) Address() string { return "" } // Address is dynamic via Start() in JS

func (p *JSDirective) Start() ([]net.Listener, error) {
	// Execute Handle script

	if p.startFn == nil {
		return []net.Listener{}, nil
	}
	res, err := p.startFn(goja.Undefined())
	if err != nil {
		return nil, err
	}
	if goja.IsString(res) {
		ln, err := net.Listen("tcp", res.String())
		if err != nil {
			return nil, err
		}
		return []net.Listener{ln}, nil
	}
	return []net.Listener{}, nil
}

func (p *JSDirective) Match(peek []byte) (bool, error) {
	// Call global match(buffer)
	buf := p.vm.NewArrayBuffer(peek)
	res, err := p.matchFn(goja.Undefined(), p.vm.ToValue(buf))
	if err != nil {
		return false, err
	}
	return res.ToBoolean(), nil
}

func (p *JSDirective) Handle(conn net.Conn) error {
	if p.handleFn == nil {
		return fmt.Errorf("handle() function not defined in script for protocol %s", p.name)
	}

	// 1. Socket Wrapper (Node.js style)
	jsSocket := NewJSSocket(conn, p.vm)
	p.vm.Set("socket", jsSocket)

	// 2. Legacy Emitter (kept for backward compatibility with first version)
	emitter := p.vm.NewObject()
	events := make(map[string][]goja.Callable)
	emitter.Set("on", func(name string, cb goja.Callable) {
		events[name] = append(events[name], cb)
	})
	emitter.Set("emit", func(name string, args ...goja.Value) {
		for _, cb := range events[name] {
			cb(goja.Undefined(), args...)
		}
	})
	p.vm.Set("emitter", emitter)

	// Execute Handle script
	_, err := p.handleFn(goja.Undefined())
	if err != nil {
		return err
	}

	// 4. Background Read Loop (The "Heart" of the async socket)
	go jsSocket.readLoop()

	return nil
}

func (p *JSDirective) HandlePacket(data []byte, addr net.Addr, pc net.PacketConn) error {
	if p.handlePacketFn == nil {
		return fmt.Errorf("handlePacket() function not defined in script for protocol %s", p.name)
	}

	buf := p.vm.NewArrayBuffer(data)
	_, err := p.handlePacketFn(goja.Undefined(), p.vm.ToValue(buf), p.vm.ToValue(addr.String()))
	return err
}

type JSSocket struct {
	conn   net.Conn
	vm     *goja.Runtime
	events map[string][]goja.Callable
	closed bool
	mu     sync.Mutex
}

func NewJSSocket(conn net.Conn, vm *goja.Runtime) *JSSocket {
	return &JSSocket{
		conn:   conn,
		vm:     vm,
		events: make(map[string][]goja.Callable),
	}
}

func (s *JSSocket) On(name string, cb goja.Callable) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events[name] = append(s.events[name], cb)
}

func (s *JSSocket) emit(name string, args ...goja.Value) {
	s.mu.Lock()
	callbacks := s.events[name]
	s.mu.Unlock()

	for _, cb := range callbacks {
		// Caution: Goja vm is not thread-safe.
		// However, for current binder implementation, we expect Handle logic to be simple.
		// TODO: In a production environment, we should synchronize with the VM loop.
		cb(goja.Undefined(), args...)
	}
}

func (s *JSSocket) Write(data interface{}) (int, error) {
	switch v := data.(type) {
	case string:
		return s.conn.Write([]byte(v))
	case []byte:
		return s.conn.Write(v)
	case goja.ArrayBuffer:
		return s.conn.Write(v.Bytes())
	default:
		return 0, fmt.Errorf("unsupported write type")
	}
}

func (s *JSSocket) End() error {
	s.closed = true
	return s.conn.Close()
}

func (s *JSSocket) Destroy() error {
	return s.End()
}

func (s *JSSocket) readLoop() {
	buf := make([]byte, 4096)
	for {
		n, err := s.conn.Read(buf)
		if n > 0 {
			data := string(buf[:n])
			s.emit("data", s.vm.ToValue(data))
		}
		if err != nil {
			if !s.closed {
				s.emit("error", s.vm.ToValue(err.Error()))
				s.emit("close")
			}
			break
		}
	}
}

func (p *JSDirective) Close() error {
	return nil
}
