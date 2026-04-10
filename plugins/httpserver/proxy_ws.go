package httpserver

import (
	"crypto/tls"
	"log"
	"strings"

	fws "github.com/fasthttp/websocket"
	"github.com/gofiber/contrib/v3/websocket"
	"github.com/gofiber/fiber/v3"
)

// WSProxy returns a fiber.Handler that proxies WebSocket requests to the target URL.
func WSProxy(target string) fiber.Handler {
	return func(c fiber.Ctx) error {
		if websocket.IsWebSocketUpgrade(c) {
			c.Locals("target_url", target)
			c.Locals("orig_url", c.OriginalURL())
			return websocket.New(proxyWSSession)(c)
		}
		return fiber.ErrUpgradeRequired
	}
}

func proxyWSSession(c *websocket.Conn) {
	origTarget := c.Locals("target_url").(string)
	origURL := c.Locals("orig_url").(string)

	dialURL := strings.TrimRight(origTarget, "/") + origURL
	if !strings.HasPrefix(dialURL, "ws://") && !strings.HasPrefix(dialURL, "wss://") {
		dialURL = "ws://" + strings.TrimPrefix(dialURL, "http://")
		dialURL = strings.Replace(dialURL, "https://", "wss://", 1)
	}

	dialer := fws.Dialer{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
	}
	// Pass headers? Some backends might need authorization headers from the original request.
	// But let's stick to the raw handshake first.
	backend, _, err := dialer.Dial(dialURL, nil)
	if err != nil {
		log.Printf("WS Proxy dial error to %s: %v", dialURL, err)
		return
	}
	defer backend.Close()

	errc := make(chan error, 2)

	// Copy from Client to Backend
	go func() {
		for {
			mt, msg, err := c.ReadMessage()
			if err != nil {
				errc <- err
				return
			}
			if err := backend.WriteMessage(mt, msg); err != nil {
				errc <- err
				return
			}
		}
	}()

	// Copy from Backend to Client
	go func() {
		for {
			mt, msg, err := backend.ReadMessage()
			if err != nil {
				errc <- err
				return
			}
			if err := c.WriteMessage(mt, msg); err != nil {
				errc <- err
				return
			}
		}
	}()

	<-errc
}
