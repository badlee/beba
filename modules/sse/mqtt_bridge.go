package sse

import (
	"fmt"
	"sync"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
)

var (
	bridgeOnce    sync.Once
	bridgeClients map[string]paho.Client
	bridgeMutex   sync.RWMutex
)

// initBridge ensures the bridge registry is initialized.
func initBridge() {
	bridgeOnce.Do(func() {
		bridgeClients = make(map[string]paho.Client)
	})
}

// getOrConnectBridge retrieves an existing pooled paho client or connects a new one.
func getOrConnectBridge(brokerURL string) (paho.Client, error) {
	initBridge()

	bridgeMutex.RLock()
	client, exists := bridgeClients[brokerURL]
	bridgeMutex.RUnlock()

	if exists && client.IsConnected() {
		return client, nil
	}

	// We need to connect (or reconnect)
	bridgeMutex.Lock()
	defer bridgeMutex.Unlock()

	// Double check
	client, exists = bridgeClients[brokerURL]
	if exists && client.IsConnected() {
		return client, nil
	}

	opts := paho.NewClientOptions()
	opts.AddBroker(brokerURL)
	opts.SetClientID(fmt.Sprintf("httpserver-bridge-%d", time.Now().UnixNano()))
	opts.SetPingTimeout(10 * time.Second)
	opts.SetKeepAlive(60 * time.Second)
	opts.SetAutoReconnect(true)

	newClient := paho.NewClient(opts)
	token := newClient.Connect()
	if token.Wait() && token.Error() != nil {
		return nil, fmt.Errorf("failed to connect to upstream bridge %s: %w", brokerURL, token.Error())
	}

	bridgeClients[brokerURL] = newClient
	return newClient, nil
}

// ForwardToBridge handles pushing a payload to a remote bridge synchronously using an isolated goroutine.
func ForwardToBridge(brokerURL, topic string, payload []byte, qos byte) {
	go func() {
		client, err := getOrConnectBridge(brokerURL)
		if err != nil {
			fmt.Printf("MQTT Bridge Connect Error: %v\n", err)
			return
		}

		token := client.Publish(topic, qos, false, payload)
		token.Wait()
		if token.Error() != nil {
			fmt.Printf("MQTT Bridge Publish Error (%s): %v\n", brokerURL, token.Error())
		}
	}()
}
