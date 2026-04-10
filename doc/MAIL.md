# MAIL — Référence DSL

## Providers supportés

| Scheme | Backend |
|---|---|
| `smtp://host:587` | SMTP (STARTTLS) |
| `smtps://host:465` | SMTP (TLS implicite) |
| `sendgrid://SG.key` | SendGrid API v3 |
| `mailgun://key@mg.domain.com` | Mailgun REST API |
| `postmark://serverToken` | Postmark API |
| `rest://https://api.example.com/send` | REST générique |

---

## Structure de base

```bind
MAIL 'smtp://smtp.host.com:587' [default]
    NAME mailer
    // directives...
END MAIL
```

`[default]` marque cette connexion comme connexion utilisée par `mail.send()` sans argument.

---

## Directives

### NAME *(obligatoire)*

```bind
NAME mailer
```

Identifiant JS — accessible via `require('mail').get('mailer')`.

### USER — Authentification SMTP

```bind
USER username password
```

### USE — Chiffrement

```bind
USE TLS    // STARTTLS (port 587)
USE SSL    // TLS implicite (port 465)
USE PLAIN  // Sans chiffrement
```

### FROM — Expéditeur par défaut

Trois formes possibles, évaluées dans l'ordre :

```bind
// 1. Statique
FROM noreply@example.com

// 2. Fichier JS — doit retourner une string
FROM "senders/from.js" [env=prod]

// 3. Inline JS — doit retourner une string
FROM BEGIN
    return email.to[0].endsWith("@vip.com")
        ? "vip@monapp.com"
        : "noreply@monapp.com"
END FROM
```

Variable disponible : `email { subject, from, to, cc, bcc, content, headers }`.

### SET — Métadonnées de connexion

```bind
SET fromName "Mon Application"
```

### TEMPLATE — Gabarits d'email

```bind
TEMPLATE welcome BEGIN
    <h1>Bienvenue {{name}} !</h1>
    <p>Merci de rejoindre <strong>{{appName}}</strong>.</p>
END TEMPLATE

TEMPLATE invoice "emails/invoice.html"
```

Rendu Mustache — les variables sont fournies via `data` dans `mail.send()`.

### PROCESSOR — Middleware avant/après envoi

```bind
PROCESSOR @PRE  "validators/spam.js" [args...]

PROCESSOR @POST BEGIN [tag=v1]
    if (!email.to.length) reject("no recipients")
    email.subject = "[APP] " + email.subject
END PROCESSOR
```

| Phase | Moment | `email.content` |
|---|---|---|
| `@PRE` *(défaut)* | Avant rendu du template | `""` (vide) |
| `@POST` | Après envoi | HTML rendu |

**Variables disponibles dans le script PROCESSOR :**

| Variable | Description |
|---|---|
| `email` | `{ subject, from, fromName, replyTo, to, cc, bcc, content, headers }` |
| `request` | `{ url, method, headers, body, query }` — REST uniquement, phase `@POST` |
| `args` | Arguments de la ligne PROCESSOR |
| `reject(msg)` | Annuler l'envoi (propagé comme erreur) |
| `done()` | Succès explicite (no-op) |

Toute mutation sur `email` est re-synchronisée dans le message interne avant envoi.

---

## Provider REST personnalisé

```bind
MAIL 'rest://https://api.provider.com/v1/send'
    NAME custom
    METHOD POST

    // Trois formes : statique, fichier JS, inline JS
    HEADER Authorization "Bearer token"
    HEADER "headers/sign.js"   [env=prod]
    HEADER BEGIN [ts=true]
        append("X-Timestamp", Date.now().toString())
    END HEADER

    BODY   source   myapp
    BODY BEGIN
        append("from", email.from)
        append("to",   email.to.join(","))
    END BODY

    QUERY  version  v2
    QUERY  "qs.js"  [env=prod]
END MAIL
```

**Variables dans les scripts HEADER / BODY / QUERY :**

| Variable | Description |
|---|---|
| `append(key, val)` | Ajouter un champ dans la map |
| `email` | L'email en cours d'envoi |
| `args` | Arguments de la ligne |

---

## Pièces jointes

```js
// Objet lazy — fichier lu au moment de l'envoi
mail.attachment("reports/q3.pdf")
mail.attachment("logo.png").name("logo-app.png").type("image/png")

// Données brutes
{ filename: "data.csv", contentType: "text/csv", data: csvString }
{ filename: "bin.bin",  data: arrayBuffer }
```

`mail.attachment(path)` retourne un descripteur chainable :

| Méthode | Description |
|---|---|
| `.name(filename)` | Renommer le fichier dans l'email |
| `.type(mimeType)` | Forcer le Content-Type |

Si le fichier est introuvable à l'envoi, l'erreur remonte dans le `.catch()`.

---

## API JavaScript — `require('mail')`

### Gestion des connexions

```js
const mail = require('mail')

// Créer une connexion à la volée
mail.connect("sendgrid://SG.key", "sg", {
    from:     "no-reply@monapp.com",
    fromName: "Mon App",
    default:  false,
})

mail.connection("sg")       // proxy d'une connexion existante
mail.connectionNames        // accessor → string[]
mail.hasConnection("sg")    // bool
mail.hasDefault             // accessor bool
mail.default                // accessor → proxy connexion par défaut
```

### Envoi

```js
// Envoi simple (connexion par défaut)
await mail.send({
    to:          "user@example.com",       // string ou string[]
    cc:          ["cc@example.com"],
    bcc:         "bcc@example.com",
    from:        "override@monapp.com",    // optionnel
    fromName:    "Override",
    replyTo:     "reply@monapp.com",
    subject:     "Sujet",
    html:        "<h1>Hello</h1>",
    text:        "Hello",                  // fallback texte
    template:    "welcome",                // nom de template DSL
    data:        { name: "Alice" },        // variables du template
    headers:     { "X-Custom": "val" },
    attachments: [
        mail.attachment("report.pdf"),
        { filename: "data.csv", data: csvString },
    ],
})

// Via une connexion nommée
await mail.connection("sg").send({ to: "u@e.com", subject: "x", html: "<b>hi</b>" })

// Thenable
mail.send({ ... })
    .then(()  => print("envoyé"))
    .catch(e  => print("erreur: " + e))
```

### Gestion des templates en JS

```js
const conn = mail.connection("mailer")

conn.setTemplate("promo", "<h1>Promo {{pct}}% !</h1>")
conn.setFileTemplate("invoice", "emails/invoice.html")
conn.deleteTemplate("promo")       // interdit si défini dans le .bind

conn.hasTemplate("welcome")        // bool
conn.templateNames                 // accessor → string[]
conn.template("welcome")           // source brute
```

Les templates définis dans le `.bind` sont **verrouillés** — toute tentative de modification depuis JS lève une erreur.

---

## Exemple complet

```bind
MAIL 'smtp://smtp.gmail.com:587' [default]
    NAME gmail
    USER moncompte@gmail.com "app-password"
    USE TLS
    FROM noreply@monapp.com
    SET fromName "Mon Application"

    PROCESSOR @PRE BEGIN
        if (!email.to || !email.to.length) reject("destinataire manquant")
        email.subject = "[MonApp] " + email.subject
    END PROCESSOR

    TEMPLATE welcome BEGIN
        <h1>Bienvenue {{name}} !</h1>
        <p>Merci de rejoindre <strong>{{appName}}</strong>.</p>
    END TEMPLATE

    TEMPLATE reset "emails/reset-password.html"
END MAIL
```

```js
const mail = require('mail')

// Email de bienvenue
await mail.send({
    to:       user.email,
    subject:  "Bienvenue",
    template: "welcome",
    data:     { name: user.name, appName: "MonApp" },
})

// Email avec pièce jointe
await mail.send({
    to:          manager.email,
    subject:     "Rapport mensuel",
    html:        "<p>Rapport en pièce jointe.</p>",
    attachments: [ mail.attachment("reports/monthly.pdf").name("rapport.pdf") ],
})
```