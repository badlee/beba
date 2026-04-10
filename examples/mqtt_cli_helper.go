package main

import (
	"bytes"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/gorilla/websocket"
)

// A tiny, manual MQTT-over-WS client for testing.

func main() {
	cmd := flag.String("cmd", "pub", "pub | sub")
	wsURL := flag.String("url", "ws://127.0.0.1:9400/mqtt", "MQTT-over-WS URL")
	topic := flag.String("topic", "test", "MQTT Topic")
	payload := flag.String("payload", "hello", "MQTT Payload (pub only)")
	timeout := flag.Int("timeout", 5, "Connection timeout in seconds")
	flag.Parse()

	u, err := url.Parse(*wsURL)
	if err != nil {
		log.Fatal(err)
	}

	header := http.Header{}
	header.Add("Sec-WebSocket-Protocol", "mqtt")

	d := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	conn, _, err := d.Dial(u.String(), header)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	// 1. Send CONNECT
	// Simple MQTT 3.1.1 CONNECT packet
	connectPkt := []byte{
		0x10,       // Fixed header: type=CONNECT
		12,         // Remaining length (variable header=10 + clientID=2)
		0x00, 0x04, // Length of "MQTT"
		'M', 'Q', 'T', 'T',
		0x04,             // Protocol level 4
		0x02,             // Connect flags: cleanSession=1
		0x00, 0x3C,       // Keepalive=60s
		0x00, 0x00,       // ClientID length=0 (auto assigned)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, connectPkt); err != nil {
		log.Fatal(err)
	}

	// Wait for CONNACK
	_, p, err := conn.ReadMessage()
	if err != nil || len(p) < 4 || p[0] != 0x20 || p[3] != 0x00 {
		log.Fatalf("expected CONNACK ok, got %X (err=%v)", p, err)
	}

	if *cmd == "pub" {
		// 2a. Send PUBLISH (QoS 0)
		tLen := len(*topic)
		pLen := len(*payload)
		remLen := 2 + tLen + pLen
		
		var pkt bytes.Buffer
		pkt.WriteByte(0x30) // Fixed header: type=PUBLISH QoS=0
		writeVarLen(&pkt, remLen)
		pkt.WriteByte(uint8(tLen >> 8))
		pkt.WriteByte(uint8(tLen & 0xFF))
		pkt.WriteString(*topic)
		pkt.WriteString(*payload)

		if err := conn.WriteMessage(websocket.BinaryMessage, pkt.Bytes()); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("✅ Published to %s: %s\n", *topic, *payload)
		time.Sleep(100 * time.Millisecond) // Wait for flush

	} else if *cmd == "sub" {
		// 2b. Send SUBSCRIBE
		tLen := len(*topic)
		remLen := 2 + 2 + tLen + 1 // msgID=2 + topicLen=2 + topic + QoS=1
		
		var pkt bytes.Buffer
		pkt.WriteByte(0x82) // Fixed header: type=SUBSCRIBE
		writeVarLen(&pkt, remLen)
		pkt.WriteByte(0x00) // msgID high
		pkt.WriteByte(0x01) // msgID low
		pkt.WriteByte(uint8(tLen >> 8))
		pkt.WriteByte(uint8(tLen & 0xFF))
		pkt.WriteString(*topic)
		pkt.WriteByte(0x00) // Requested QoS 0

		if err := conn.WriteMessage(websocket.BinaryMessage, pkt.Bytes()); err != nil {
			log.Fatal(err)
		}

		// Wait for SUBACK
		_, p, err = conn.ReadMessage()
		if err != nil || p[0] != 0x90 {
			log.Fatalf("expected SUBACK, got %X", p)
		}

		fmt.Printf("⌛ Subscribed to %s, waiting for message (%ds)...\n", *topic, *timeout)
		
		// Wait for PUBLISH
		conn.SetReadDeadline(time.Now().Add(time.Duration(*timeout) * time.Second))
		for {
			_, p, err = conn.ReadMessage()
			if err != nil {
				fmt.Println("❌ Timeout or error")
				os.Exit(1)
			}
			if p[0]&0xF0 == 0x30 {
				// Parse simple PUBLISH
				tLenIn := int(p[2])<<8 | int(p[3])
				msg := string(p[4+tLenIn:])
				fmt.Printf("✅ Received: %s\n", msg)
				return
			}
		}
	}
}

func writeVarLen(b *bytes.Buffer, l int) {
	for {
		digit := uint8(l % 128)
		l /= 128
		if l > 0 {
			digit |= 0x80
		}
		b.WriteByte(digit)
		if l == 0 {
			break
		}
	}
}
