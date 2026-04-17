package sse

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	fastwebsocket "github.com/fasthttp/websocket"
	fiberwebsocket "github.com/gofiber/contrib/v3/websocket"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

const (
	wsWriteTimeout = 10 * time.Second
	wsPingInterval = 25 * time.Second
	wsReadTimeout  = 60 * time.Second
)

// WSUpgradeMiddleware refuse les requêtes non-WebSocket.
func WSUpgradeMiddleware(c fiber.Ctx) error {
	if fiberwebsocket.IsWebSocketUpgrade(c) {
		// Preserve params for the upgraded connection context
		c.Locals("ws_query_id", c.Query("id"))
		c.Locals("ws_param_id", c.Params("id"))
		c.Locals("ws_query_channel", c.Query("channel"))
		c.Locals("ws_param_channel", c.Params("channel"))
		return c.Next()
	}
	return fiber.ErrUpgradeRequired
}

type WSCtx struct {
	*fiberwebsocket.Conn
	mu sync.Mutex
}

func (c *WSCtx) SafeWrite(mt int, payload []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
	return c.WriteMessage(mt, payload)
}

func (c *WSCtx) Params(key string, defaultValue ...string) string {
	if v := c.Locals("ws_param_" + key); v != nil {
		return v.(string)
	}
	return ""
}

func (c *WSCtx) Query(key string, defaultValue ...string) string {
	if v := c.Locals("ws_query_" + key); v != nil {
		return v.(string)
	}
	return ""
}

func (c *WSCtx) Cookies(key string, defaultValue ...string) string {
	// Cookies are typically not available after upgrade unless passed via Locals
	if v := c.Locals("cookie_" + key); v != nil {
		return v.(string)
	}
	return ""
}

func (c *WSCtx) Locals(key any, defaultValue ...any) any {
	k, ok := key.(string)
	if !ok {
		return nil
	}
	return c.Conn.Locals(k, defaultValue...)
}

// WSHandler gère une connexion WebSocket en réutilisant Hub, Client et Message.
func WSHandler(conn *fiberwebsocket.Conn, runner ...*ScriptedRunner) {
	wsCtx := &WSCtx{Conn: conn}
	sid, channels := parseChannels(wsCtx)

	ctx, cancel := context.WithCancel(context.Background())
	client := &Client{
		sid:      sid,
		ConnID:   uuid.New().String(),
		message:  make(chan *Message, clientBuf),
		channels: channels,
		ctx:      ctx,
		cancel:   cancel,
	}

	for _, ch := range channels {
		HubInstance.Subscribe(client, ch)
	}

	var scripted *Runtime
	if len(runner) > 0 && runner[0] != nil {
		r := runner[0]
		scripted = NewRuntime(client, func(channel, data string) error {
			// Publish to Hub with loop prevention
			HubInstance.Publish(&Message{
				Channel:   channel,
				Data:      data,
				Source:    "js",
				SenderSID: client.ConnID,
			})
			// Also deliver back to the socket for point-to-point
			payload, _ := json.Marshal(Message{Channel: channel, Data: data})
			return wsCtx.SafeWrite(fastwebsocket.TextMessage, payload)
		}, func() {
			cancel()
			client.closed.Store(true)
			conn.Close()
		}, wsCtx)

		if err := scripted.Run(r.Code, ".", r.Config); err != nil {
			log.Printf("WS JS runtime error: %v", err)
		}
	}

	defer func() {
		// Shutdown() queues "close" + sentinel, waits for onClose to run in the
		// eventLoop goroutine (thread-safe for the JS VM), then returns.
		if scripted != nil {
			scripted.Shutdown()
		}
		cancel()
		client.closed.Store(true)
		for _, ch := range client.channels {
			HubInstance.Unsubscribe(client, ch)
		}
		conn.Close()
	}()

	// --- Goroutine d'écriture : hub → WebSocket ---
	writeDone := make(chan struct{})
	go func() {
		defer close(writeDone)
		pingTicker := time.NewTicker(wsPingInterval)
		defer pingTicker.Stop()

		for {
			select {
			case msg, ok := <-client.message:
				if !ok {
					return
				}
				if msg.Event == "heartbeat" {
					// Ignoré : car le ping natif WS suffit
					continue
				}
				
				if scripted != nil && scripted.HasSub(msg.Channel) {
					scripted.Emit("hub_message", msg)
				} else {
					payload, err := json.Marshal(msg) // Message est directement sérialisable
					if err != nil {
						continue
					}
					if err := wsCtx.SafeWrite(fastwebsocket.TextMessage, payload); err != nil {
						return
					}
				}

			case <-pingTicker.C:
				if err := wsCtx.SafeWrite(fastwebsocket.PingMessage, nil); err != nil {
					return
				}

			case <-ctx.Done():
				return
			}
		}
	}()

	// --- Boucle de lecture : WebSocket → hub ---
	conn.SetReadDeadline(time.Now().Add(wsReadTimeout))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(wsReadTimeout))
		return nil
	})

	for {
		mt, raw, err := conn.ReadMessage()
		if err != nil {
			if scripted != nil {
				scripted.Emit("error", err.Error())
			}
			break
		}

		if scripted != nil {
			if mt == fastwebsocket.BinaryMessage {
				scripted.Emit("message", raw) // binary -> ArrayBuffer
			} else {
				scripted.Emit("message", string(raw)) // text -> string
			}
		}

		if mt != fastwebsocket.TextMessage {
			continue // Standard Hub routing ignores binary
		}

		var msg Message
		if err := json.Unmarshal(raw, &msg); err != nil {
			if scripted == nil {
				log.Printf("ws: json invalide depuis %s: %v", sid, err)
			}
			continue
		}

		// Security: In Passive Mode (no script), only allow publishing to subscribed channels
		if scripted == nil {
			if msg.Channel == "" {
				msg.Channel = "global"
			}
			if !client.HasChannel(msg.Channel) {
				log.Printf("ws: security block - client %s tried to publish to unauthorized channel %s", sid, msg.Channel)
				continue
			}
		}

		msg.Source = "ws"
		msg.SenderSID = client.ConnID
		HubInstance.Publish(&msg)
	}

	// Trigger write goroutine to exit (breaks the deadlock with <-writeDone)
	cancel()
	<-writeDone
}
