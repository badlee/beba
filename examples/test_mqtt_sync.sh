#!/bin/bash

# MQTT-Hub Synchronization Test
# 
# Requires: ./http-server --bind examples/mqtt_sync.bind
#
# This script validates bidirectional synchronization between SSE and MQTT.

PORT=9400
MQTT_URL="ws://127.0.0.1:$PORT/api/realtime/mqtt"
SSE_URL="http://127.0.0.1:$PORT/api/realtime/sse"
PUB_URL="http://127.0.0.1:$PORT/api/publish"

echo "--- MQTT-Hub Synchronization Test ---"
echo "Server Target: $PORT"
echo ""

# 0. Check if server is running
curl -s -o /dev/null --connect-timeout 2 "http://127.0.0.1:$PORT/"
if [ $? -ne 0 ]; then
    echo "❌ Error: Server not found on :$PORT"
    echo "Please run: ./http-server --bind examples/mqtt_sync.bind"
    exit 1
fi

echo "✅ Server is online."

# 1. Test MQTT -> Hub Sync
echo "[1] Testing MQTT -> Hub (SSE) Sync..."
TEMP_LOG=$(mktemp)

# Start background SSE listener (listen for 3 seconds)
# Subscribe to "home/temp" specifically
curl -s -N "$SSE_URL?channel=home/temp" > "$TEMP_LOG" &
SSE_PID=$!
sleep 2

# Publish via MQTT CLI
go run examples/mqtt_cli_helper.go -cmd pub -topic "home/temp" -payload "23.5"
sleep 1

kill $SSE_PID 2>/dev/null
wait $SSE_PID 2>/dev/null

grep "data: 23.5" "$TEMP_LOG" > /dev/null
if [ $? -eq 0 ]; then
    echo "✅ MQTT message correctly appeared in Hub (SSE)!"
else
    echo "❌ Hub (SSE) failed to receive MQTT message."
    # cat "$TEMP_LOG"
fi
rm "$TEMP_LOG"

# 2. Test Hub -> MQTT Sync
echo ""
echo "[2] Testing Hub (API) -> MQTT Sync..."

# Start MQTT subscriber to wait for a message
# Give it more time to connect and subscribe
go run examples/mqtt_cli_helper.go -cmd sub -topic "home/alerts" -timeout 10 &
SUB_PID=$!
sleep 3

# Publish via Hub API
curl -s -X POST -H "Content-Type: application/json" \
     -d '{"topic": "home/alerts", "payload": "FIRE!"}' \
     "$PUB_URL" > /dev/null

wait $SUB_PID
if [ $? -eq 0 ]; then
    echo "✅ Hub message correctly appeared in MQTT Subscriber!"
else
    echo "❌ MQTT failed to receive Hub message (timeout)."
fi

echo ""
echo "--- Tests Completed ---"
