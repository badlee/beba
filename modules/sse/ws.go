package sse

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/gofiber/contrib/v3/websocket"
	"github.com/gofiber/fiber/v3"
)

const (
	wsWriteTimeout = 10 * time.Second
	wsPingInterval = 25 * time.Second
	wsReadTimeout  = 60 * time.Second
)

// WSUpgradeMiddleware refuse les requêtes non-WebSocket.
func WSUpgradeMiddleware(c fiber.Ctx) error {
	if websocket.IsWebSocketUpgrade(c) {
		return c.Next()
	}
	return fiber.ErrUpgradeRequired
}

type WSCtx struct {
	*websocket.Conn
}

func (c *WSCtx) Params(key string, defaultValue ...string) string {
	return c.Conn.Params(key, defaultValue...)
}

func (c *WSCtx) Query(key string, defaultValue ...string) string {
	return c.Conn.Query(key, defaultValue...)
}

func (c *WSCtx) Cookies(key string, defaultValue ...string) string {
	return c.Conn.Cookies(key, defaultValue...)
}

func (c *WSCtx) Locals(key any, defaultValue ...any) any {
	k, ok := key.(string)
	if !ok {
		return nil
	}
	return c.Conn.Locals(k, defaultValue...)
}

// WSHandler gère une connexion WebSocket en réutilisant Hub, Client et Message.
// Format JSON échangé : {"channel":"...","event":"...","data":"..."}
// Le champ channel est optionnel en lecture (défaut : "global").
func WSHandler(conn *websocket.Conn) {
	sid, channels := parseChannels(&WSCtx{conn})

	ctx, cancel := context.WithCancel(context.Background())
	client := &Client{
		sid:      sid,
		message:  make(chan *Message, clientBuf),
		channels: channels,
		ctx:      ctx,
		cancel:   cancel,
	}

	for _, ch := range channels {
		HubInstance.Subscribe(client, ch)
	}

	defer func() {
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
				payload, err := json.Marshal(msg) // Message est directement sérialisable
				if err != nil {
					continue
				}
				conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
				if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
					return
				}

			case <-pingTicker.C:
				conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
				if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
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
			break
		}
		if mt != websocket.TextMessage {
			continue
		}

		var msg Message
		if err := json.Unmarshal(raw, &msg); err != nil {
			log.Printf("ws: json invalide depuis %s: %v", sid, err)
			continue
		}
		if msg.Channel == "" {
			msg.Channel = "global"
		}

		HubInstance.Publish(&msg)
	}

	<-writeDone
}
