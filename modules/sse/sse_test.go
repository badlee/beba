package sse

import (
	"context"
	"testing"
	"time"

	"github.com/dop251/goja"
	"github.com/gofiber/fiber/v3"
)

// -------------------- TESTS --------------------

// TestPublishGlobal vérifie que publish() diffuse à tous les abonnés du channel "global".
func TestPublishGlobal(t *testing.T) {
	vm := goja.New()
	sse := sseObj(t, vm, map[string]string{"sid": "s1"})

	ch1 := newTestClient(t, "s1", []string{"global", "sid:s1"})
	ch2 := newTestClient(t, "s2", []string{"global", "sid:s2"})

	// Laisser les subscribe se propager dans les goroutines des shards
	time.Sleep(10 * time.Millisecond)

	callJS(t, vm, sse, "publish", "evt", "hello_global")

	msg1 := recv(t, ch1, "client1 global")
	if msg1.Data != "hello_global" {
		t.Errorf("client1: attendu hello_global, reçu %q", msg1.Data)
	}

	msg2 := recv(t, ch2, "client2 global")
	if msg2.Data != "hello_global" {
		t.Errorf("client2: attendu hello_global, reçu %q", msg2.Data)
	}
}

// TestSendIsolation vérifie que send() ne délivre le message qu'au client ciblé par son sid.
func TestSendIsolation(t *testing.T) {
	vm := goja.New()

	// fiberMockCtx satisfait fiber.Ctx → isFiberCtx = true dans Loader
	var _ fiber.Ctx = (*fiberMockCtx)(nil)
	mock := newFiberMock(map[string]string{"sid": "target"})
	mod := &Module{}
	export := vm.NewObject()
	mod.Loader(mock, vm, export)
	var sse *goja.Object
	if exp := export.Get("exports"); exp != nil && !goja.IsUndefined(exp) {
		sse = exp.ToObject(vm)
	} else {
		sse = export
	}

	chTarget := newTestClient(t, "target", []string{"global", "sid:target"})
	chOther := newTestClient(t, "other", []string{"global", "sid:other"})

	time.Sleep(10 * time.Millisecond)

	// attach("target") → mock.Locals("sse_sid", "target")
	// send("evt","private") → Publish sur "sid:target"
	callJS(t, vm, sse, "attach", "target")
	callJS(t, vm, sse, "send", "evt", "private")

	msg := recv(t, chTarget, "target reçoit")
	if msg.Data != "private" {
		t.Errorf("target: attendu private, reçu %q", msg.Data)
	}
	noRecv(t, chOther, "other ne reçoit pas")
}

// TestToPublish vérifie que to(channel).publish() cible un channel nommé.
func TestToPublish(t *testing.T) {
	vm := goja.New()
	sse := sseObj(t, vm, nil)

	chNews := newTestClient(t, "reader", []string{"news"})
	chOther := newTestClient(t, "other", []string{"global", "sid:other"})

	time.Sleep(10 * time.Millisecond)

	pubObj := sse.Get("to").(*goja.Object)
	if pubObj == nil {
		t.Fatal("to() n'a pas retourné d'objet")
	}
	// to("news") retourne un objet avec publish()
	toFn, _ := goja.AssertFunction(sse.Get("to"))
	result, err := toFn(goja.Undefined(), vm.ToValue("news"))
	if err != nil {
		t.Fatalf("to(): %v", err)
	}
	callJS(t, vm, result.(*goja.Object), "publish", "evt", "news_item")

	msg := recv(t, chNews, "news subscriber")
	if msg.Data != "news_item" {
		t.Errorf("news: attendu news_item, reçu %q", msg.Data)
	}

	noRecv(t, chOther, "other ne reçoit pas news")
}

// TestParseChannels vérifie la déduplication et le parsing de parseChannels.
func TestParseChannels(t *testing.T) {
	cases := []struct {
		name              string
		cookies           map[string]string
		query             string
		wantSIDInChannels bool
		wantGlobal        bool
	}{
		{
			name:              "cookie sid",
			cookies:           map[string]string{"sid": "abc"},
			wantSIDInChannels: true,
			wantGlobal:        true,
		},
		{
			name:              "no cookie — uuid généré",
			cookies:           nil,
			wantSIDInChannels: true,
			wantGlobal:        true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newFiberMock(tc.cookies)
			sid, channels := parseChannels(c)

			if sid == "" {
				t.Error("sid vide")
			}

			hasGlobal := false
			hasSid := false
			for _, ch := range channels {
				if ch == "global" {
					hasGlobal = true
				}
				if ch == "sid:"+sid {
					hasSid = true
				}
			}
			if tc.wantGlobal && !hasGlobal {
				t.Error("channel global manquant")
			}
			if tc.wantSIDInChannels && !hasSid {
				t.Errorf("channel sid:%s manquant dans %v", sid, channels)
			}

			// Vérification déduplication
			seen := map[string]int{}
			for _, ch := range channels {
				seen[ch]++
				if seen[ch] > 1 {
					t.Errorf("channel dupliqué: %q", ch)
				}
			}
		})
	}
}

// TestClientIsolation vérifie qu'un client fermé ne reçoit plus de messages.
func TestClientIsolation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan *Message, 32)
	client := &Client{
		sid:      "zombie",
		message:  ch,
		channels: []string{"global"},
		ctx:      ctx,
		cancel:   cancel,
	}
	HubInstance.Subscribe(client, "global")
	time.Sleep(10 * time.Millisecond)

	// Marquer comme fermé sans unsubscribe — le shard doit ignorer ce client
	client.closed.Store(true)
	cancel()

	HubInstance.Publish(&Message{Channel: "global", Event: "evt", Data: "ghost"})

	noRecv(t, ch, "client fermé ne reçoit pas")

	// Cleanup
	HubInstance.Unsubscribe(client, "global")
}

// TestTopicCleanup vérifie que le ring buffer (Topic) est libéré quand il n'y a plus d'abonnés.
func TestTopicCleanup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	channel := "temp_channel"
	client := &Client{
		sid:      "temp",
		message:  make(chan *Message, 1),
		channels: []string{channel},
		ctx:      ctx,
		cancel:   cancel,
	}

	// 1. S'abonner et vérifier la création du topic
	HubInstance.Subscribe(client, channel)
	time.Sleep(10 * time.Millisecond)

	shard := HubInstance.shard(channel)
	if _, ok := shard.topics[channel]; !ok {
		t.Fatalf("Le topic %s n'a pas été créé lors de l'abonnement", channel)
	}

	// 2. Se désabonner et vérifier la suppression (mémoire libérée)
	HubInstance.Unsubscribe(client, channel)
	time.Sleep(10 * time.Millisecond)

	if _, ok := shard.topics[channel]; ok {
		t.Fatalf("Le topic %s a fui ; il aurait dû être supprimé d'après sse.go", channel)
	}
}
