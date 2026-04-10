package crud

import (
	"encoding/json"
	"http-server/modules/sse"
	"testing"
	"time"
)

func TestBroadcastCRUDIntegration(t *testing.T) {
	if sse.HubInstance == nil {
		t.Fatal("SSE HubInstance is nil")
	}

	sid := "test-client-1"
	// Subscribing to different level of granularity
	channels := []string{
		"crud::create",
		"crud:ns1:update",
		"crud:ns1:schema1:delete",
		"crud:ns1:schema1:doc1:read",
	}
	client := sse.NewClient(sid, channels)
	for _, ch := range channels {
		sse.HubInstance.Subscribe(client, ch)
	}
	defer client.Close()

	// Wait for subscriptions to propagate through shards
	time.Sleep(100 * time.Millisecond)

	// Helper to receive and parse
	recv := func(t *testing.T, timeout time.Duration) map[string]any {
		select {
		case msg := <-client.Messages():
			var m map[string]any
			if err := json.Unmarshal([]byte(msg.Data), &m); err != nil {
				t.Fatalf("Failed to decode SSE data: %v", err)
			}
			m["_channel"] = msg.Channel
			return m
		case <-time.After(timeout):
			t.Fatal("Timeout waiting for SSE message")
			return nil
		}
	}

	// 1. Test global create broadcast
	broadcastCRUD("create", "ns1", "schema1", "doc1", map[string]any{"foo": "bar"})
	msg := recv(t, 500*time.Millisecond)
	if msg["_channel"] != "crud::create" {
		t.Errorf("Expected channel crud::create, got %v", msg["_channel"])
	}
	if msg["action"] != "create" || msg["id"] != "doc1" {
		t.Errorf("Unexpected payload mapping: %v", msg)
	}

	// 2. Test update with diff and prev
	prev := map[string]any{"name": "Alice", "age": 30, "status": "active"}
	next := map[string]any{"name": "Alice", "age": 31} // status removed, age changed
	
	broadcastCRUD("update", "ns1", "schema1", "doc1", next, prev)
	
	// We might receive multiple messages if we were subscribed to crud::update too,
	// but we only subscribed to crud:ns1:update for updates.
	msg = recv(t, 500*time.Millisecond)
	if msg["_channel"] != "crud:ns1:update" {
		t.Errorf("Expected channel crud:ns1:update, got %v", msg["_channel"])
	}
	
	if msg["prev"] == nil || msg["diff"] == nil {
		t.Fatal("Expected prev and diff in update payload")
	}
	
	diff := msg["diff"].(map[string]any)
	if diff["age"] != float64(31) { // json decode numbers as float64
		t.Errorf("Expected age 31 in diff, got %v", diff["age"])
	}
	if _, ok := diff["name"]; ok {
		t.Errorf("name should NOT be in diff, it didn't change")
	}
	if _, ok := diff["status"]; !ok {
		// My computeDiff sets missing keys to nil
		t.Errorf("status should be in diff (as nil), it was removed")
	}

	// 3. Test specific document read
	broadcastCRUD("read", "ns1", "schema1", "doc1", map[string]any{"meta": "ok"})
	msg = recv(t, 500*time.Millisecond)
	if msg["_channel"] != "crud:ns1:schema1:doc1:read" {
		t.Errorf("Expected specific doc channel, got %v", msg["_channel"])
	}
}
