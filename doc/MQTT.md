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
- **WebSockets (WSS)**: Automatically enabled when the `MQTT` directive is placed inside an `HTTP`/`HTTPS` block. The standard path is **`/api/realtime/mqtt`**.

---

## 🏗️ Security Interoperability
Parce que `http-server` utilise un modèle de défense en profondeur, le **Broker MQTT** est protégé par la **couche Sentinel (L1-L4)** avant même que le premier paquet MQTT ne soit parsé.
- **Déni de Connexion** : Les IPs bloquées ou les pays restreints par GeoIP sont rejetés dès l'étape `net.Accept`.
- **Mitigation DDoS** : La directive `CONNECTION RATE` empêche le broker d'être submergé par des tentatives de `CONNECT` rapides.

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

        # L5: Persistance (QoS 1 & 2)
        STORAGE mqtt_db
        
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
END TCP
```

---

## 🛠️ Directives

### 1. `STORAGE [DBName]`
Active l'atomicité et la persistance pour les messages **QoS 1 & 2**, les messages retenus et les sessions persistantes.

### 2. `ACL DEFINE`
Contrôle d'accès granulaire aux topics.

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

## 🔗 Le Pont SSE/MQTT (Unified Hub)

Le hub fournit un pont natif entre le SSE (navigateur) et le MQTT (industriel) :
- **Broadcasting** : Tout message publié sur un topic MQTT est automatiquement émis sous forme d'événement SSE vers les clients abonnés au même canal.
- **Path Web-Standard** : Le broker est accessible via WebSocket sur **`/api/realtime/mqtt`**.

---

## 🧪 DSL Complet

```hcl
MQTT [address]?
    // Configuration du moteur (mochi-mqtt)
    OPTIONS DEFINE
        MAX_CLIENTS [int]
        MESSAGE_EXPIRY [duration] // 1m, 3h, 2j...
        WRITES_PENDING [int]
        SESSION_EXPIRY [duration]
        MAX_PACKET_SIZE [int]
        MAX_PACKETS [int]
        MAX_RECEIVE [int]
        MAX_INFLIGHT [int]
        MAX_ALIAS [int]
        AVAILABLE_SHARED_SUB [ON|OFF]
        MIN_PROTOCOL [3|5]
        RETAIN [ON|OFF]
        HAS_WILDCARD_SUB [ON|OFF]
    END OPTIONS

    // Persistance (Sessions, Retains)
    STORAGE [db_name]

    // Sécurité L4 réutilisant un bloc SECURITY
    SECURITY [security_name]

    // Authentification
    AUTH USER [USER_NAME] [PWD] 
    AUTH HANDLER [filepath]
    AUTH BEGIN 
        /* code JS : allow(), reject(msg) */
    END AUTH
    
    // Contrôle d'accès (ACL)
    ACL ALLOW ANY READ "public/#"
    ACL DENY ANY WRITE "firmware/updates/+"
    ACL BEGIN 
        /* code JS : allow(), reject(msg) */
    END ACL

    // Les HOOKS (Événements)
    ON [EVENT_TYPE] [event_name] HANDLER [js_filepath]
    ON [EVENT_TYPE] [event_name] BEGIN 
        /* code JS */
    END ON
END MQTT
```
