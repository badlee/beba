# Socket.IO Module (Method "IO")

Le `http-server` intègre un support Socket.IO qui est entièrement unifié avec le Hub SSE/WS. Cela permet une communication temps-réel bidirectionnelle entre les clients Socket.IO et tous les autres protocoles supportés (SSE, WebSockets classiques, MQTT).

## 🚀 Configuration Binder

Pour activer Socket.IO sur une route spécifique, utilisez la méthode `IO` dans votre fichier `.bind`.

```hcl
HTTP 0.0.0.0:8080
    # Active Socket.IO sur le chemin /socket.io
    IO /socket.io
END HTTP
```

### Paramètres et Authentification

La méthode `IO` supporte les middlewares nommés pour l'authentification et d'autres fonctionnalités :

```hcl
HTTP 0.0.0.0:8080
    # Avec authentification admin requise
    IO @AUTH[redirect="/login"] /sio
END HTTP
```

## 📡 Mapping Socket.IO ↔ Hub SSE

Le module Socket.IO est nativement connecté au Hub SSE global :

- **Événements entrants (Client → Serveur)** :
  Le client envoie un objet JSON :
  ```json
  {"channel": "chat", "event": "message", "data": "Hello world"}
  ```
  Le message est automatiquement publié sur le Hub dans le canal `chat`. Il sera reçu par tous les abonnés (SSE, WS, MQTT, SIO).

- **Événements sortants (Serveur → Client)** :
  Tout message publié sur le Hub dans un canal auquel le client est abonné lui est envoyé au format :
  ```json
  {"channel": "global", "event": "updates", "data": "..."}
  ```

## 🔐 Canaux et Abonnements

À la connexion, un client Socket.IO est automatiquement abonné aux canaux suivants :
1. `global` : Canal public par défaut.
2. `sid:<uuid>` : Canal privé unique correspondant à l'UUID généré par Socket.IO.
3. `sid:<id>` : Canal privé personnalisé si un `sid` a été positionné via `c.Locals("sid")` (par exemple via un cookie ou un JWT) ou via un paramètre d'URL si la route est définie comme `IO /sio/:id`.

### Abonnements dynamiques

Le client peut s'abonner ou se désabonner de canaux dynamiquement en envoyant des messages spéciaux :

- **S'abonner** : `{"action": "subscribe", "channel": "news"}`
- **Se désabonner** : `{"action": "unsubscribe", "channel": "news"}`

## 🛠️ Usage Côté Client (JavaScript)

Bien que compatible avec le protocole WebSocket sous-jacent, le module est conçu pour être utilisé avec une approche simple de type message JSON.

```javascript
const socket = new WebSocket('ws://localhost:8080/socket.io');

socket.onopen = () => {
    // S'abonner à un canal
    socket.send(JSON.stringify({
        action: "subscribe",
        channel: "chat"
    }));

    // Envoyer un message
    socket.send(JSON.stringify({
        channel: "chat",
        event: "msg",
        data: "Hello!"
    }));
};

socket.onmessage = (event) => {
    const msg = JSON.parse(event.data);
    console.log(`Reçu sur ${msg.channel} [${msg.event}]: ${msg.data}`);
};
```

## ⚡ API Native (Backend)

Vous pouvez interagir avec les clients Socket.IO depuis votre code natif ou vos scripts JS :

- **Publication globale** : `sse.SIOPublish("news", "alert", "Important update")`
- **Envoi direct (Point-à-point)** : `sse.SIOSend(uuid, "private", "Hello you")`
- **Broadcast système** : `sse.SIOBroadcast("shutdown", "Server restarting")`
