package binder

import (
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"http-server/modules/sse"
	"http-server/processor"

	"github.com/google/uuid"
	"github.com/limba/dtp/pkg/dtp"
	"github.com/limba/dtp/pkg/dtpserver"
)

type DTPDirective struct {
	server  *sse.DTPServer
	address string
	cfg     *DirectiveConfig
}

func NewDTPDirective(config *DirectiveConfig) (*DTPDirective, error) {
	// The DTP server needs an address to refer to, although here we use it logic-only
	// or we let it handle the net.Conn directly.
	// sessions timeout can be configured via Settigns if needed
	timeout := config.Configs.GetDuration("DTP_SESSION_TIMEOUT", 5*time.Minute)

	srv := sse.NewDTPServer(timeout)
	inner := srv.Server()

	// Configure Events from config.Events
	events := config.GetRoutes("EVENT")
	for _, ev := range events {
		event := ev // capture
		inner.On(dtp.TypeEvent, func(device *dtpserver.DeviceConfig, pkt *dtp.Packet) {
			vm := processor.New(config.BaseDir, nil, config.AppConfig)
			// Expose device and packet to JS
			vm.Set("device", device)
			vm.Set("packet", pkt)
			vm.Set("payload", string(pkt.Payload))

			if event.Inline {
				vm.RunString(event.Handler)
			} else {
				content, err := os.ReadFile(event.Handler)
				if err == nil {
					vm.RunString(string(content))
				}
			}
		}, dtp.TypeEvent.SubTypeFromString(event.Path))
	}

	// Configure Device Auth via AUTH directive
	cache := new(sync.Map)
	if len(config.Auth) > 0 {
		// Additional validation during authentication
		inner.OnAuthDevice = func(authPayload *dtp.AuthPayload) error {
			deviceID, err := uuid.Parse(authPayload.DeviceID)
			if err != nil {
				return fmt.Errorf("invalid device ID: %w", err)
			}
			_, ok := cache.Load(deviceID)
			if !ok {
				return fmt.Errorf("device not found")
			}
			return nil
		}
		inner.OnGetDevice = func(deviceID string) *dtpserver.DeviceConfig {
			info, err := config.Auth.UserInfo(deviceID)
			if err != nil {
				return nil
			}
			id, err := uuid.Parse(deviceID)
			if err != nil {
				return nil
			}
			s := &dtpserver.DeviceConfig{
				DeviceID:     id,
				Secret:       []byte(info.Pwd()),
				UseProtoBuff: info.Proto(),
			}
			cache.Store(deviceID, s)
			return s
		}
	} else {
		inner.OnAuthDevice = func(authPayload *dtp.AuthPayload) error {
			return fmt.Errorf("device not found")
		}
		inner.OnGetDevice = func(deviceID string) *dtpserver.DeviceConfig {
			return nil
		}
	}
	for _, route := range config.Routes {
		r := route // capture
		switch r.Method {
		case "PING":
			inner.On(dtp.TypePing, func(device *dtpserver.DeviceConfig, pkt *dtp.Packet) {
				vm := processor.New(config.BaseDir, nil, config.AppConfig)
				// Expose device and packet to JS
				vm.Set("device", device)
				vm.Set("packet", pkt)
				vm.Set("payload", string(pkt.Payload))

				content, err := r.Content(config)
				if err == nil {
					vm.RunString(string(content))
				}
			}, dtp.TypePing.SubTypeFromString(r.Path))
		case "PONG":
			inner.On(dtp.TypePong, func(device *dtpserver.DeviceConfig, pkt *dtp.Packet) {
				vm := processor.New(config.BaseDir, nil, config.AppConfig)
				// Expose device and packet to JS
				vm.Set("device", device)
				vm.Set("packet", pkt)
				vm.Set("payload", string(pkt.Payload))

				content, err := r.Content(config)
				if err == nil {
					vm.RunString(string(content))
				}
			}, dtp.TypePong.SubTypeFromString(route.Path))
		case "CMD":
			inner.On(dtp.TypeCmd, func(device *dtpserver.DeviceConfig, pkt *dtp.Packet) {
				vm := processor.New(config.BaseDir, nil, config.AppConfig)
				// Expose device and packet to JS
				vm.Set("device", device)
				vm.Set("packet", pkt)
				vm.Set("payload", string(pkt.Payload))

				content, err := r.Content(config)
				if err == nil {
					vm.RunString(string(content))
				}
			}, dtp.TypeCmd.SubTypeFromString(route.Path))
		case "DATA":
			inner.On(dtp.TypeData, func(device *dtpserver.DeviceConfig, pkt *dtp.Packet) {
				vm := processor.New(config.BaseDir, nil, config.AppConfig)
				// Expose device and packet to JS
				vm.Set("device", device)
				vm.Set("packet", pkt)
				vm.Set("payload", string(pkt.Payload))

				content, err := r.Content(config)
				if err == nil {
					vm.RunString(string(content))
				}
			}, dtp.TypeData.SubTypeFromString(route.Path))
		case "ACK":
			inner.On(dtp.TypeACK, func(device *dtpserver.DeviceConfig, pkt *dtp.Packet) {
				vm := processor.New(config.BaseDir, nil, config.AppConfig)
				// Expose device and packet to JS
				vm.Set("device", device)
				vm.Set("packet", pkt)
				vm.Set("payload", string(pkt.Payload))

				content, err := r.Content(config)
				if err == nil {
					vm.RunString(string(content))
				}
			}, dtp.TypeACK.SubTypeFromString(route.Path))
		case "NACK":
			inner.On(dtp.TypeNACK, func(device *dtpserver.DeviceConfig, pkt *dtp.Packet) {
				vm := processor.New(config.BaseDir, nil, config.AppConfig)
				// Expose device and packet to JS
				vm.Set("device", device)
				vm.Set("packet", pkt)
				vm.Set("payload", string(pkt.Payload))

				content, err := r.Content(config)
				if err == nil {
					vm.RunString(string(content))
				}
			}, dtp.TypeNACK.SubTypeFromString(route.Path))
		case "ERR":
			inner.On(dtp.TypeError, func(device *dtpserver.DeviceConfig, pkt *dtp.Packet) {
				vm := processor.New(config.BaseDir, nil, config.AppConfig)
				// Expose device and packet to JS
				vm.Set("device", device)
				vm.Set("packet", pkt)
				vm.Set("payload", string(pkt.Payload))

				content, err := r.Content(config)
				if err == nil {
					vm.RunString(string(content))
				}
			}, dtp.TypeError.SubTypeFromString(route.Path))
		case "QUEUE":
			r := route // capture
			// Handle message queuing for offline or "sync" mode devices.
			inner.OnQueue = func(deviceID uuid.UUID, msgType dtp.Type, subtype dtp.Subtype, payload []byte) error {
				vm := processor.New(config.BaseDir, nil, config.AppConfig)
				vm.Set("deviceID", deviceID)
				vm.Set("msgType", msgType)
				vm.Set("subtype", subtype)
				vm.Set("payload", string(payload))
				content, err := r.Content(config)
				if err == nil {
					vm.RunString(string(content))
				}
				return nil
			}
		case "ONLINE":
			inner.OnSessionStatus = func(deviceID uuid.UUID, online bool) {
				status := "offline"
				if online {
					status = "online"
				}
				sse.HubInstance.Publish(&sse.Message{
					Channel: "dtp.session.status",
					Event:   status,
					Data:    deviceID.String(),
				})
			}
		}
	}

	return &DTPDirective{
		server:  srv,
		address: config.Address,
		cfg:     config,
	}, nil
}

func (d *DTPDirective) Name() string    { return "DTP" }
func (d *DTPDirective) Address() string { return d.address }

func (d *DTPDirective) Start() ([]net.Listener, error) {
	ln, err := net.Listen("tcp", d.address)
	if err != nil {
		return nil, err
	}
	return []net.Listener{ln}, nil
}

func (d *DTPDirective) Match(peek []byte) (bool, error) {
	if len(peek) == 0 {
		return false, nil
	}
	// DTP packets start with Version0/Version1 (0x01 0x00)
	return dtp.IsValid(peek), nil
}

func (d *DTPDirective) Handle(conn net.Conn) error {
	// Let the DTP server handle the connection
	// We use the now-exported HandleConnection method from Limba's DTP Server.
	d.server.HandleConnection(conn)
	return nil
}

func (d *DTPDirective) HandlePacket(data []byte, addr net.Addr, pc net.PacketConn) error {
	// For UDP, DTP has a specific way of handling packets
	// We check if it's a UDPConn for now
	if udpConn, ok := pc.(*net.UDPConn); ok {
		if udpAddr, ok := addr.(*net.UDPAddr); ok {
			// Call the internal UDP handler from sse.DTPServer
			d.server.HandlePacket(udpConn, udpAddr, data)
			return nil
		}
	}
	return fmt.Errorf("unsupported packet connection type")
}

func (d *DTPDirective) Close() error {
	// Cleanup DTP server if needed
	return nil
}
