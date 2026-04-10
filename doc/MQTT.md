# Protocol: MQTT
**Syntax:** `MQTT [address]?`

Le projet intègre nativement le moteur ultra-performant `mochi-mqtt` version 2.  
Cette directive convertit instantanément un bloc `TCP` ou le multiplexeur global en un **Broker MQTT de production**.

---

### Architecture de Sécurité (Sniffing Non-Destructif)
Contrairement aux proxys TCP classiques, le `Binder` utilise un mécanisme de **non-destructive protocol sniffing** (`bufio.Peek`). 
Cela permet :
1. D'analyser le paquet `CONNECT` initial sans le consommer.
2. D'appliquer les politiques `SECURITY` (IP Filtering, Geo-Block, Rate Limit) **AVANT** de passer la main au broker.
3. De garantir que le handshake MQTT complet est reçu par le moteur `mochi-mqtt` sans perte d'octets.
4. L'injection s'effectue via l'API `EstablishConnection`, garantissant une gestion des cycles de vie robuste et isolée pour chaque client.

---

### Configuration Rapide

```hcl
TCP :1883
    MQTT :1883
        # Active la protection par mot de passe globale (Basique Auth)
        AUTH admin password123
        
        # Stocke les messages QoS 1 & 2 dans GORM via SQLite/Postgres
        # Nécessite un bloc DATABASE préalablement défini
        STORAGE localDB
        
        # Applique une politique de sécurité globale (Connection-level)
        SECURITY mqtt_firewall
        
        # Logique dynamique via Hooks JS (ACL, OnConnect, OnPublish)
        OPTIONS mqtt_logic.js
    END MQTT
END TCP
```

---

### Directives Supportées

1. **`STORAGE [DBAlias]`**  
   Utilise une connexion **GORM** pour la persistence. 
   - **Résolution Globale** : Le `DBAlias` peut faire référence à une base de donnée déclarée via `DATABASE` ou initialisée automatiquement par le module **`CRUD`**.
   - **QoS 1 & 2** : Les messages non-acquittés sont sauvegardés en DB de manière atomique.
   - **Sessions** : Les abonnements des clients persistants survivent au redémarrage du serveur.
   - **Migration** : Les tables `mqtt_clients`, `mqtt_retained`, etc. sont auto-migrées au démarrage du broker.

2. **`SECURITY [PolicyName]`**  
   **Isolation au niveau Socket.** Votre courtier MQTT profite d'une encapsulation réseau complète.  
   Avant même que le handshake MQTT ne commence, l'IP est inspectée. Si la politique `SECURITY` bloque la connexion, le socket est fermé immédiatement par le `Manager`.

3. **`BRIDGE [Url]`**  
   Relai asynchrone local (Edge Node) renvoyant les topics vers un nœud Cloud distant (HiveMQ, AWS IoT).

4. **`AUTH [User] [Pass]`**  
   Authentification statique simple. Pour une gestion dynamique (ex: via DB), utilisez les `OPTIONS` avec des hooks JS.

5. **`OPTIONS [script.js]`**  
   Attache des Hooks JavaScript pour étendre la logique (ACL, Auth dynamique, OnPublish).

---

### Le Pont Bidirectionnel (SSE <-> MQTT)

Le `Hub SSE` et le `Broker MQTT` partagent le même bus de données interne via `HubInstance`.
- **MQTT -> SSE** : Un message publié sur `sensor/temp` est automatiquement diffusé aux clients HTTP écoutant sur `/sse?channel=sensor/temp`.
- **SSE -> MQTT** : Un message envoyé via l'API SSE ou un Hook JS vers un channel est re-publié sur le topic MQTT correspondant (QoS 0 par défaut).

---

### Tests et Validation
L'architecture MQTT est validée par une suite de tests d'intégration complète (`modules/sse/mqtt_integration_test.go`) garantissant :
- L'isolation stricte des bases de données par test (`t.TempDir()`).
- Le bon fonctionnement des directives `SECURITY` et `STORAGE` via des fichiers `.bind` (`tests/mqtt/`).
- La persistance des messages QoS 1 après redémarrage.
