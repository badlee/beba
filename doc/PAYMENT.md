# PAYMENT — Module de Paiement

Le module `PAYMENT` intègre un système de paiement complet avec support natif de **Stripe**, **Mobile Money** (MTN, Orange) et de **providers custom** entièrement configurables via le DSL Binder.

## Principe

Le module est déclaré dans un bloc `PAYMENT ... END PAYMENT` et monté sur un serveur HTTP via la directive `PAYMENT name /prefix` dans un bloc `HTTP`.

```bind
PAYMENT 'stripe://sk_live_xxx' [default]
    NAME stripe
    MODE sandbox
    CURRENCY EUR
    CALLBACK "https://myapp.com/api/payment/webhook"
    REDIRECT success "/merci"
    REDIRECT cancel  "/annule"
    REDIRECT failure "/echec"
END PAYMENT

HTTP :8080
    PAYMENT stripe /pay
END HTTP
```

---

## Directives

### NAME *(obligatoire)*
Identifiant de l'instance. Utilisé dans `PAYMENT name /prefix` et `require('payment')`.

### MODE
```bind
MODE sandbox       // ou production
```
Définit le mode d'exécution (sandbox/test vs production).

### CURRENCY / COUNTRY
```bind
CURRENCY XOF       // devise par défaut
COUNTRY CM         // code ISO pays (pour MoMo)
```

### CALLBACK
URL asynchrone de confirmation (webhook).

### REDIRECT
URLs de redirection après un checkout :
```bind
REDIRECT success "/merci"
REDIRECT cancel  "/annule"
REDIRECT failure "/echec"
```

---

## Providers Natifs

### Stripe
Connexion via URI `stripe://sk_xxx`.
Supporte : `charge`, `verify`, `refund`, `checkout`.

### Mobile Money (MTN / Orange)
Connexion via URI `momo://subscription_key`.
Supporte : `charge` (USSD push), `verify`.

---

## Provider Custom

Pour les fournisseurs non supportés nativement, le DSL permet de définir chaque opération :

```bind
PAYMENT 'custom'
    NAME mypay
    CURRENCY XOF

    CHARGE DEFINE
        ENDPOINT "https://api.mypay.com/v1/charges"
        METHOD POST
        HEADER Authorization "Bearer key"
        BODY BEGIN
            append("amount",    payment.amount)
            append("currency",  payment.currency)
            append("reference", payment.orderId)
        END BODY
        RESPONSE BEGIN
            if (response.status !== 200) reject(response.body.message)
            resolve({ id: response.body.transactionId, status: "pending" })
        END RESPONSE
    END CHARGE

    VERIFY DEFINE
        ENDPOINT "https://api.mypay.com/v1/charges/{id}"
        METHOD GET
        HEADER Authorization "Bearer key"
        RESPONSE BEGIN
            resolve({ id: response.body.id, status: response.body.state })
        END RESPONSE
    END VERIFY

    REFUND DEFINE
        ENDPOINT "https://api.mypay.com/v1/refunds"
        METHOD POST
        BODY BEGIN
            append("transactionId", payment.id)
            append("amount",        payment.amount)
        END BODY
        RESPONSE BEGIN
            resolve({ id: response.body.refundId, status: "refunded" })
        END RESPONSE
    END REFUND

    CHECKOUT DEFINE
        ENDPOINT "https://api.mypay.com/v1/checkout"
        METHOD POST
        BODY BEGIN
            append("success_url", payment.redirects.success)
            append("cancel_url",  payment.redirects.cancel)
            append("amount",      payment.amount)
        END BODY
        RESPONSE BEGIN
            resolve({ redirectUrl: response.body.checkoutUrl, id: response.body.sessionId })
        END RESPONSE
    END CHECKOUT

    USSD DEFINE
        ENDPOINT "https://api.mypay.com/v1/ussd"
        METHOD POST
        BODY BEGIN
            append("phone",  payment.phone)
            append("amount", payment.amount)
        END BODY
        RESPONSE BEGIN
            if (response.status !== 202) reject("USSD push failed")
            resolve({ id: response.body.requestId, status: "pending" })
        END RESPONSE
    END USSD
END PAYMENT
```

### Sous-directives Custom

| Directive | Description |
|---|---|
| `ENDPOINT` | URL de l'API (`{id}` interpolé automatiquement) |
| `METHOD` | Méthode HTTP (GET, POST, etc.) |
| `HEADER key value` | En-tête statique (ou script JS inline/fichier) |
| `BODY BEGIN...END BODY` | Corps de la requête (script JS, appel `append(k,v)`) |
| `QUERY key value` | Paramètre de query string |
| `RESPONSE BEGIN...END RESPONSE` | Script de traitement de la réponse (`resolve()` / `reject()`) |

---

## Webhooks

```bind
WEBHOOK @PRE /pay/webhook BEGIN [secret=xxx]
    if (!verify(request.body, request.headers["stripe-signature"], args.secret))
        reject("bad signature")
END WEBHOOK

WEBHOOK @POST /pay/webhook BEGIN
    if (payment.status === "succeeded") { /* traitement */ }
END WEBHOOK
```

| Phase | Description |
|---|---|
| `@PRE` | Validation avant traitement (signature, intégrité) |
| `@POST` | Logique métier après validation |

---

## API JavaScript — `require('payment')`

```js
const pay = require('payment')           // connexion par défaut
const sg  = require('payment').get('stripe')

// Initier un paiement
const result = pay.charge({
    amount: 5000, currency: "XOF",
    phone: "237612345678", email: "u@e.com",
    orderId: "ORD-001",
    metadata: { description: "Achat produit" },
})
// result = { id, status, redirectUrl? }

// Vérifier un statut
const status = pay.verify("txn_abc123")

// Rembourser
pay.refund({ id: "txn_abc123", amount: 2500 })

// Checkout (page de paiement hébergée)
const checkout = pay.checkout({ amount: 5000, orderId: "ORD-002" })
// checkout.redirectUrl → rediriger le client

// USSD push
const push = pay.ussd({ phone: "237612345678", amount: 1000, orderId: "ORD-003" })

// Gestion des connexions
pay.connection("stripe")
pay.connectionNames      // accessor
pay.hasConnection("mtn") // bool
pay.hasDefault           // accessor bool
pay.default              // accessor → proxy connexion par défaut

pay.connect("stripe://sk_test_xxx", "stripe2", { currency: "EUR" })
```

---

## Routes HTTP générées

Lorsque monté via `PAYMENT name /prefix` :

| Méthode | Chemin | Description |
|---|---|---|
| `POST` | `/prefix/charge` | Initier un paiement |
| `GET` | `/prefix/verify/:id` | Vérifier le statut |
| `POST` | `/prefix/refund` | Rembourser |
| `POST` | `/prefix/checkout` | Créer une session checkout |
| `POST` | `/prefix/ussd` | Push USSD |
| `POST` | `/prefix/webhook` | Webhook de confirmation |
