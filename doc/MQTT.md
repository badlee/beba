# Protocol: MQTT (Unified Real-time Hub)

The `MQTT` directive transforms a port or a global multiplexer into a high-performance **MQTT v5 Broker** powered by `mochi-mqtt`. It is a core component of the **Unified Real-time Hub**, sharing a common data bus with SSE, WebSocket, and DTP.

---

## 🏗️ Architecture

### Multi-Protocol Multiplexing
Thanks to **non-destructive protocol sniffing** (`bufio.Peek`), the server can distinguish MQTT traffic from HTTP or custom protocols on the same port.
1.  **Handshake Sniffing**: The `Manager` detects the `CONNECT` packet.
2.  **Sentinel Security**: Policies (`IP`, `Geo`, `Rate Limit`) are enforced **before** the broker accepts the connection.
3.  **Bridge Execution**: Messages are dispatched to the internal pub/sub bus, allowing seamless SSE/MQTT cross-communication.

### WebSocket Support
MQTT is accessible via:
- **Native TCP**: Default (usually port 1883).
- **WebSockets (WSS)**: Automatically enabled when the `MQTT` directive is placed inside an `HTTPS` block or when using the `@mqtt` middleware on a specific route.

---


## 🏗️ Security Interoperability
Because `http-server` uses a defense-in-depth model, the **MQTT Broker** is protected by the **Sentinel Layer (L1-L4)** even before the first MQTT packet is parsed.
- **Connection Denial**: Blocked IPs or Geo-restricted countries are rejected at the `net.Accept` stage.
- **DDoS Mitigation**: The `CONNECTION RATE` directive prevents the broker from being overwhelmed by rapid `CONNECT` attempts.
- **Sniffing Security**: Using `bufio.Peek`, the security engine inspects the headers of a connection without consuming it, allowing the MQTT broker to receive the full handshake if allowed.

## 🚀 Configuration

```hcl
DATABASE "sqlite://mqtt_data.db" [default]
    NAME "mqtt_db"
END DATABASE

SECURITY iot_shield
    CONNECTION RATE 100r/s 1s burst=10
    CONNECTION ALLOW "192.168.1.0/24"
END SECURITY

MQTT :1883
    # L1: Socket Security
    SECURITY iot_shield

    # L5: Persistence (QoS 1 & 2)
    STORAGE default
    
    # Auth & ACLs
    AUTH "admin" "secret_pass"
    ACL DEFINE
        ALLOW ALL READ "public/#"
        ALLOW USER "admin" WRITE "#"
    END ACL

    # Advanced MQTT v5 Capabilities
    OPTIONS DEFINE
        MAX_CLIENTS 5000
        MESSAGE_EXPIRY 24h
        SESSION_EXPIRY 7d
        MAX_PACKET_SIZE 65535
        RETAIN ON
    END OPTIONS

    # JS Logic
    ON PUBLISH @processor "logic.js"
END MQTT
```

---

## 🛠️ Directives

### 1. `STORAGE [DBName]`
Enables atomicity and persistence for **QoS 1 & 2** messages, retained messages, and persistent sessions.
- Requires a defined `DATABASE`.
- Automatically migrates tables: `mqtt_clients`, `mqtt_subscriptions`, `mqtt_retained`, `mqtt_inflight`.

### 2. `ACL DEFINE`
Granular access control for topics.
```hcl
ACL DEFINE
    ALLOW ALL READ "sensors/+"
    DENY  USER "guest" WRITE "sensors/#"
    ALLOW IP "10.0.0.1" ALL "#"
END ACL
```

### 3. `OPTIONS DEFINE`
Fine-tune the broker's behavior and MQTT v5 capabilities:
- `MAX_CLIENTS [int]`: Maximum concurrent connections.
- `MESSAGE_EXPIRY [duration]`: TTL for messages (e.g., `24h`).
- `SESSION_EXPIRY [duration]`: TTL for persistent sessions (e.g., `7d`).
- `MAX_PACKET_SIZE [bytes]`: Limit individual packet size.
- `MAX_QOS [0|1|2]`: Enforce a maximum QoS level.
- `RETAIN [ON|OFF]`: Enable or disable message retention.
- `MIN_PROTOCOL [3|4|5]`: Minimum MQTT version (3=v3.1, 4=v3.1.1, 5=v5).

---

## 🔗 The SSE/MQTT Bridge

The hub provides a native bridge between browser-based SSE and industrial MQTT:
- **Broadcasting**: Any message published to an MQTT topic is automatically emitted as an SSE event to clients subscribed to the same channel name.
- **Injection**: Using `require('sse').publish(topic, payload)`, you can inject data into the MQTT broker directly from a JS handler.

---

## 🧪 Validation
The MQTT module is verified against:
- **Persistence Leak-testing**: Ensuring DB size remains stable under high volume.
- **Security Interception**: Validating that blocked IPs never trigger a `CONNECT` response.
- **Protocol Switching**: Testing seamless transitions between TCP and WebSocket clients.
