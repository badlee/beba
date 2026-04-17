package sse

// socketio.go — Intégration Socket.IO (gofiber/contrib/v3/socketio) avec le Hub SSE.
//
// # Mapping Socket.IO ↔ Hub
//
//   Message entrant (client → serveur) :
//     {"channel":"news","event":"article","data":"hello"}
//     → HubInstance.Publish(&Message{Channel:"news", Event:"article", Data:"hello"})
//     → reçu par TOUS les abonnés SSE, WS, MQTT abonnés à "news"
//
//   Message sortant (Hub → client) :
//     Hub publie &Message{Channel:"global", Event:"evt", Data:"val"}
//     → tous les clients Socket.IO abonnés à "global" reçoivent :
//     {"channel":"global","event":"evt","data":"val"}
//
// # Abonnements
//
//   À la connexion, le client est abonné automatiquement à :
//     - "global"
//     - "sid:<uuid>"   (canal privé via son UUID socketio)
//     - "sid:<id>"     (si l'attribut "sid" est positionné avant la connexion via c.Locals)
//
//   Le client peut s'abonner/désabonner dynamiquement en envoyant :
//     {"action":"subscribe",   "channel":"news"}
//     {"action":"unsubscribe", "channel":"news"}
//
// # Usage dans main.go
//
//   app.Use("/sio", func(c fiber.Ctx) error {
//       if websocket.IsWebSocketUpgrade(c) {
//           // optionnel : pré-positionner un sid depuis un cookie/JWT
//           c.Locals("sid", c.Cookies("sid"))
//           return c.Next()
//       }
//       return fiber.ErrUpgradeRequired
//   })
//   app.Get("/sio",     sse.SIOHandler())
//   app.Get("/sio/:id", sse.SIOHandler())

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/gofiber/contrib/v3/socketio"
	"github.com/gofiber/contrib/v3/websocket"
	"github.com/gofiber/fiber/v3"
	guuid "github.com/google/uuid"
)

// ==================== REGISTRY Socket.IO ====================

// sioRegistry maintient le mapping uuid → *Client Hub pour chaque connexion Socket.IO.
// Nécessaire pour retrouver le Client Hub lors du Disconnect et désabonner proprement.
type sioRegistry struct {
	mu      sync.RWMutex
	clients map[string]*sioEntry // uuid socketio → entrée
}

type sioEntry struct {
	hubClient *Client
	cancel    context.CancelFunc
	channels  []string // canaux Hub auxquels ce client est abonné
	scripted  bool     // mode scripté vs passif
	runtime   *Runtime // JS runtime (nil si pas de script)
}

var sioReg = &sioRegistry{clients: make(map[string]*sioEntry)}

func (r *sioRegistry) add(uuid string, e *sioEntry) {
	r.mu.Lock()
	r.clients[uuid] = e
	r.mu.Unlock()
}

func (r *sioRegistry) get(uuid string) (*sioEntry, bool) {
	r.mu.RLock()
	e, ok := r.clients[uuid]
	r.mu.RUnlock()
	return e, ok
}

func (r *sioRegistry) remove(uuid string) {
	r.mu.Lock()
	delete(r.clients, uuid)
	r.mu.Unlock()
}

// addChannel ajoute un canal à l'entrée (sans doublon).
func (e *sioEntry) addChannel(ch string) bool {
	for _, c := range e.channels {
		if c == ch {
			return false // déjà abonné
		}
	}
	e.channels = append(e.channels, ch)
	return true
}

// removeChannel retire un canal de l'entrée.
func (e *sioEntry) removeChannel(ch string) {
	for i, c := range e.channels {
		if c == ch {
			e.channels = append(e.channels[:i], e.channels[i+1:]...)
			return
		}
	}
}

// ==================== MESSAGE FORMAT ====================

// sioMessage est le format JSON échangé entre le client Socket.IO et le serveur.
//
// Entrant :
//
//	{"channel":"global","event":"chat","data":"hello"}
//	{"action":"subscribe","channel":"news"}
//	{"action":"unsubscribe","channel":"news"}
//
// Sortant :
//
//	{"channel":"global","event":"chat","data":"hello"}
type sioMessage struct {
	Action  string `json:"action,omitempty"`  // "subscribe" | "unsubscribe" (entrant uniquement)
	Channel string `json:"channel,omitempty"` // canal Hub cible
	Event   string `json:"event,omitempty"`   // nom de l'événement
	Data    string `json:"data,omitempty"`    // payload
}

// ==================== HANDLER ====================

// SIOHandler retourne un handler Fiber enregistrant les événements Socket.IO
// et connectant chaque socket au Hub SSE.
func SIOHandler(cfg ...any) func(fiber.Ctx) error {
	// We use any for cfg to avoid confusion with websocket.Config if we want to pass ScriptedRunner
	var runner *ScriptedRunner
	var wsCfg websocket.Config

	for _, c := range cfg {
		if r, ok := c.(*ScriptedRunner); ok {
			runner = r
		} else if w, ok := c.(websocket.Config); ok {
			wsCfg = w
		}
	}

	// ---- Connexion ----
	socketio.On(socketio.EventConnect, func(ep *socketio.EventPayload) {
		kws := ep.Kws
		uuid := kws.UUID

		// Résoudre le sid
		sid := ""
		if v := kws.Locals("sid"); v != nil {
			if s, ok := v.(string); ok && s != "" {
				sid = s
			}
		}
		if sid == "" {
			if s := kws.Params("id", ""); s != "" {
				sid = s
			}
		}
		if sid == "" {
			sid = uuid
		}

		kws.SetAttribute("sid", sid)

		// Créer le Client Hub
		hubCtx, hubCancel := context.WithCancel(context.Background())
		hubClient := &Client{
			sid:      sid,
			ConnID:   guuid.New().String(),
			message:  make(chan *Message, clientBuf),
			channels: []string{},
			ctx:      hubCtx,
			cancel:   hubCancel,
		}

		// Canaux initiaux
		initialChannels := []string{"global", "sid:" + uuid}
		if sid != uuid {
			initialChannels = append(initialChannels, "sid:"+sid)
		}
		for _, ch := range initialChannels {
			HubInstance.Subscribe(hubClient, ch)
			hubClient.channels = append(hubClient.channels, ch)
		}

		entry := &sioEntry{
			hubClient: hubClient,
			cancel:    hubCancel,
			channels:  append([]string{}, initialChannels...),
			scripted:  runner != nil,
		}
		sioReg.add(uuid, entry)

		var scripted *Runtime
		if runner != nil {
			scripted = NewRuntime(hubClient, func(channel, data string) error {
				HubInstance.Publish(&Message{
					Channel:   channel,
					Event:     "message",
					Data:      data,
					Source:    "js",
					SenderSID: hubClient.ConnID,
				})
				// Also emit back to the socket for point-to-point
				raw, _ := json.Marshal(sioMessage{Channel: channel, Event: "message", Data: data})
				return socketio.EmitTo(uuid, raw)
			}, func() {
				hubCancel()
				hubClient.closed.Store(true)
				kws.Close()
			}, kws)

			if err := scripted.Run(runner.Code, ".", runner.Config); err != nil {
				log.Printf("IO JS runtime error: %v", err)
			}
			entry.runtime = scripted
		}

		// Goroutine Hub → Socket.IO : lit les messages Hub et les envoie au client
		go func() {
			pingTicker := time.NewTicker(25 * time.Second)
			defer pingTicker.Stop()
			for {
				select {
				case <-hubCtx.Done():
					return
				case msg, ok := <-hubClient.message:
					if !ok {
						return
					}
					if msg.Event == "heartbeat" {
						continue // le keepalive WS natif suffit
					}
					// fix: unified routing — let the runtime decide which callbacks to trigger
					if entry.runtime != nil {
						entry.runtime.Emit("hub_message", msg)
					} else {
						raw, err := json.Marshal(sioMessage{
							Channel: msg.Channel,
							Event:   msg.Event,
							Data:    msg.Data,
						})
						if err != nil {
							continue
						}
						// EmitTo envoie au socket identifié par son UUID
						_ = socketio.EmitTo(uuid, raw)
					}
				case <-pingTicker.C:
					// Pas de ping manuel nécessaire : socketio gère le keepalive
				}
			}
		}()
	})

	// ---- Message reçu ----
	socketio.On(socketio.EventMessage, func(ep *socketio.EventPayload) {
		uuid := ep.SocketUUID
		entry, ok := sioReg.get(uuid)
		if !ok {
			return
		}

		var msg sioMessage
		if err := json.Unmarshal(ep.Data, &msg); err != nil {
			log.Printf("sio: json invalide depuis %s: %v", uuid, err)
			return
		}

		// Gestion des abonnements dynamiques
		switch msg.Action {
		case "subscribe":
			if msg.Channel == "" {
				return
			}
			// Éviter les doublons via addChannel
			if entry.addChannel(msg.Channel) {
				HubInstance.Subscribe(entry.hubClient, msg.Channel)
				entry.hubClient.channels = append(entry.hubClient.channels, msg.Channel)
			}
			return

		case "unsubscribe":
			if msg.Channel == "" {
				return
			}
			entry.removeChannel(msg.Channel)
			HubInstance.Unsubscribe(entry.hubClient, msg.Channel)
			return
		}

		// Pas d'action → message à publier dans le Hub
		if entry.runtime != nil {
			// Scripted mode: forward raw payload to onMessage
			entry.runtime.Emit("message", string(ep.Data))
			return
		}

		if !entry.scripted {
			if msg.Channel == "" {
				msg.Channel = "global"
			}
			if msg.Event == "" {
				return
			}
			if !entry.hubClient.HasChannel(msg.Channel) {
				log.Printf("sio: security block - client %s tried to publish to unauthorized channel %s", uuid, msg.Channel)
				return
			}
		}

		HubInstance.Publish(&Message{
			Channel:   msg.Channel,
			Event:     msg.Event,
			Data:      msg.Data,
			Source:    "sio",
			SenderSID: entry.hubClient.ConnID,
		})
	})

	// ---- Déconnexion ----
	socketio.On(socketio.EventDisconnect, func(ep *socketio.EventPayload) {
		uuid := ep.SocketUUID
		entry, ok := sioReg.get(uuid)
		if !ok {
			return
		}

		// 1. JS Lifecycle: Shutdown runtime (fires onClose)
		// Important: Shutdown MUST happen before Hub unsubscription so that
		// the onClose script can still publish to the hub.
		if entry.runtime != nil {
			entry.runtime.Shutdown()
		}

		// 2. Stop the traffic: Cancel context and unsubscribe
		entry.cancel()
		entry.hubClient.closed.Store(true)
		for _, ch := range entry.channels {
			HubInstance.Unsubscribe(entry.hubClient, ch)
		}

		sioReg.remove(uuid)
	})

	// ---- Erreur ----
	socketio.On(socketio.EventError, func(ep *socketio.EventPayload) {
		if ep.Error != nil {
			log.Printf("sio: error socket=%s: %v", ep.SocketUUID, ep.Error)
		}
		if entry, ok := sioReg.get(ep.SocketUUID); ok && entry.runtime != nil {
			if ep.Error != nil {
				entry.runtime.Emit("error", ep.Error.Error())
			} else {
				entry.runtime.Emit("error", "unknown")
			}
			entry.runtime.Shutdown()
		}
	})

	return socketio.New(func(kws *socketio.Websocket) {
		// socketio.New gère la boucle de lecture interne et fire les événements On().
		// Le callback reçu ici s'exécute pendant toute la durée de la connexion.
		// On bloque jusqu'à ce que le hub client soit fermé (déconnexion).
		if entry, ok := sioReg.get(kws.UUID); ok {
			<-entry.hubClient.ctx.Done()
		}
	}, wsCfg)
}

// ==================== API PUBLIQUE ====================

// SIOPublish publie un message dans le Hub depuis n'importe quel endroit du code.
// Tous les clients Socket.IO, SSE, WS et MQTT abonnés au channel le recevront.
//
//	sse.SIOPublish("news", "article", "Bonjour le monde")
func SIOPublish(channel, event, data string) {
	HubInstance.Publish(&Message{
		Channel: channel,
		Event:   event,
		Data:    data,
	})
}

// SIOSend envoie un message directement à un socket identifié par son UUID
// (sans passer par le Hub — livraison point-à-point garantie).
//
//	sse.SIOSend(uuid, "notification", "Vous avez un message")
func SIOSend(socketUUID, event, data string) error {
	raw, err := json.Marshal(sioMessage{
		Channel: "sid:" + socketUUID,
		Event:   event,
		Data:    data,
	})
	if err != nil {
		return err
	}
	return socketio.EmitTo(socketUUID, raw)
}

// SIOBroadcast envoie un message JSON brut à toutes les connexions Socket.IO actives.
// Utilisé pour des broadcasts système hors Hub (ex: message d'arrêt serveur).
func SIOBroadcast(event, data string) {
	raw, _ := json.Marshal(sioMessage{Event: event, Data: data})
	socketio.Fire(event, raw)
}
