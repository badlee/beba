# Device Transfer Protocol (DTP)

Le Device Transfer Protocol (DTP) est un protocole de communication binaire léger conçu pour les objets connectés (IoT). Il est intégré nativement dans `beba`, supportant les transports **TCP** et **UDP** avec un multiplexage transparent et une Bridge automatique vers le Hub SSE.

---

## Architecture de Transport

DTP peut être servi via des blocs `TCP` ou `UDP` dans vos fichiers `.bind`.

### Exemple TCP (Synchrone / Statefull)
```hcl
TCP 0.0.0.0:8001
    DTP
        AUTH CSV "devices.csv"
        EVENT "STATUS" BEGIN
            // Logic Javascript
        END EVENT
    END DTP
END TCP
```

### Exemple UDP (Asynchrone / Sans état)
```hcl
UDP 0.0.0.0:8002
    DTP
        SECURITY "@GEO[allow=FR]"
    END DTP
END UDP
```

---

## Bridge SSE Automatique

L'une des fonctionnalités les plus puissantes de l'intégration native est le **Bridge automatique vers le Hub SSE**. Chaque paquet valide reçu par le serveur DTP est instantanément retransmis sur le hub temps-réel.

### Canaux de Publication
- **Canal Appareil** : `dtp.device.<device_uuid>`
- **Canal Global** : `dtp.all` (utile pour le monitoring global)

### Format des Événements
Les événements publiés sur le Hub suivent le pattern `{TYPE}.{SUBTYPE}`.
- Ex: `DATA.GENERIC`, `EVENT.SENSOR`, `CMD.REBOOT`.

---

## Configuration du Routage

Le bloc `DTP` permet d'associer des scripts JavaScript à des types de paquets ou des subtypes spécifiques.

| Directive | Description |
|---|---|
| `DATA [subtype]` | Handler pour les messages de données (Type 0x01). |
| `CMD [subtype]` | Handler pour les commandes (Type 0x06). |
| `EVENT [subtype]`| Handler pour les alertes/événements (Type 0x07). |
| `PING` | Handler pour les messages de battement de cœur (Type 0x04). |
| `PONG` | Handler pour les réponses de battement de cœur (Type 0x05). |
| `ACK` / `NACK` | Handlers pour les acquittements (Type 0x02/0x03). |
| `ERR` | Handler pour les messages d'erreur du protocole (Type 0x08). |
| `QUEUE` | Handler pour la mise en file d'attente (messages persistants). |
| `ONLINE` | Hook de statut de session (publie sur `dtp.session.status`). |

### Subtypes
Les subtypes peuvent être spécifiés par leur nom (ex: `SENSOR_DATA`) ou leur code hexadécimal (ex: `0x01`).

```hcl
DTP
    # Handler spécifique pour les alertes fraudes
    EVENT "FRAUD" BEGIN
        log("ALERTE FRAUDE sur device " + device.DeviceID);
    END EVENT

    # Handler générique pour les données capteurs
    DATA 0x01 HANDLER "scripts/process_sensor.js"

    # Handler pour les PINGS
    PING BEGIN
        print("Ping reçu de " + device.DeviceID);
        // La réponse PONG est automatique si non gérée, mais peut être personnalisée ici
    END PING
END DTP
```

---

## Sécurité & Authentification

### Filtrage L1-L4
DTP bénéficie du moteur `SECURITY` unifié.
- **TCP** : Filtrage à l'acceptation de la session (Baseline 100r/s).
- **UDP** : Filtrage par paquet via `AllowPacket`.

### Authentification
DTP s'appuie sur la directive `AUTH` pour authentifier les appareils via leur `DeviceID` et leur `Secret`.

```hcl
DTP
    AUTH USER "00112233-4455-6677-8899-AABBCCDDEEFF" "mon_secret_propre"
END DTP
```

---

## Structure du Paquet Binaire

DTP utilise un entête fixe de 20 octets (TCP) ou 36 octets (UDP incluant ID appareil).

| Champ | Taille | Description |
|---|---|---|
| `VER0` / `VER1` | 2 Octets | Version du protocole (**0x01 0x00**). |
| `TYPE` | 1 Octet | Type de message (Data, ACK, Ping, Event...). |
| `SUBT` | 1 Octet | Sous-type du message. |
| `FLAGS`| 1 Octet | Drapeaux de contrôle (Chiffrement, Compression). |
| `EXTRA`| 1 Octet | Données d'extension. |
| `LENGTH`| 2 Octets | Longueur du Payload (Big Endian). |
| `CHECKSUM`| 4 Octets | CRC32 ou MAC HMAC-SHA256. |
| `PAYLOAD`| N Octets| Données utiles. |

---

## Client DTP JavaScript
Pour simuler ou interagir avec des serveurs DTP, utilisez le module natif `dtp`.

```javascript
const dtp = require("dtp");
const client = dtp.newClient("127.0.0.1:8001", "device_id", "secret");

client.on("connect", () => {
    client.sendData("GENERIC", JSON.stringify({ temp: 22.5 }));
});

client.connect();
```

> [!TIP]
> Consultez [doc/BINDER.md](BINDER.md) pour plus de détails sur le multiplexage TCP/UDP.
