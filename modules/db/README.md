# Module Database (Mongoose-like)

Le module `db` fournit une interface de gestion de base de données relationnelle (SQL) inspirée de **Mongoose**. Il permet de définir des schémas, des modèles et d'effectuer des opérations CRUD avec une syntaxe familière aux développeurs Node.js.

## Connexion

Le module prend en charge plusieurs systèmes de base de données via des URLs :

```javascript
const db = require('db');

// SQLite
const conn = db.connect('sqlite:///ma_base.db');

// Mémoire (SQLite)
const memoryConn = db.connect(':memory:');

// PostgreSQL
const pgConn = db.connect('postgres://user:pass@localhost:5432/mydb');

// MySQL
const mysqlConn = db.connect('mysql://user:pass@localhost:3306/mydb');
```

## Schémas

Un schéma définit la structure des documents dans une collection (table).

```javascript
const userSchema = new conn.Schema({
    name: { type: 'string', required: true },
    email: { type: 'string', unique: true },
    age: 'number',
    roles: 'array'
});

// Virtuals (Getters/Setters)
userSchema.virtual('fullName').get(function() {
    return this.firstName + ' ' + this.lastName;
});

// Middleware (Hooks)
userSchema.pre('save', function() {
    console.log('Avant la sauvegarde de ' + this.name);
});

// Méthodes d'instance
userSchema.methods.sayHello = function() {
    return "Hello, I am " + this.name;
};

// Méthodes statiques
userSchema.statics.findByEmail = function(email) {
    return this.findOne({ email: email });
};
```

## Modèles

Les modèles sont des constructeurs compilés à partir de définitions de schémas.

```javascript
const User = conn.model('User', userSchema);

// Création d'un document
const newUser = new User({ name: 'Alice', age: 30 });
newUser.save();

// Création directe
User.create({ name: 'Bob', age: 25 });
```

## Requêtes (Queries)

Le module supporte le chaînage de requêtes similaire à Mongoose.

```javascript
// Recherche avec filtres
User.find({ age: { $gt: 18 } })
    .sort('-age')
    .limit(10)
    .select('name email')
    .exec()
    .then(users => {
        console.log(users);
    });

// Recherche unique
const user = await User.findOne({ email: 'alice@example.com' });

// Utilisation de méthodes statiques personnalisées
const userByEmail = await User.findByEmail('alice@example.com');
```

#### Synchronisation et Promises
Les requêtes supportent le chaînage fluide et peuvent être exécutées de deux manières :
- `.exec()` : Retourne directement les résultats (bloquant pour le script JS courant).
- `await query` : Les objets Query sont des "thenables", permettant l'utilisation directe de `await` ou `.then()`.

---

### Opérateurs supportés

#### Opérateurs de comparaison
- `$eq` : Égalité (optionnel si valeur directe).
- `$gt`, `$gte` : Plus grand que (ou égal).
- `$lt`, `$lte` : Plus petit que (ou égal).
- `$ne` : Différent de.
- `$in`, `$nin` : Présent (ou absent) dans une liste.

#### Opérateurs logiques (Récursifs)
- `$or` : Union de plusieurs conditions (OU).
- `$and` : Intersection de plusieurs conditions (ET).
- `$nor` : Négation d'un OU (ni l'un ni l'autre).
- `$not` : Négation d'une expression.

#### Opérateurs d'élément
- `$exists` : Vérifie si le champ est présent (non NULL).
- `$type` : Vérifie le type de donnée SQLite (via `typeof()`).

#### Opérateurs d'évaluation
- `$mod` : Modulo (ex: `{ age: { $mod: [10, 5] } }` pour les âges finissant par 5).
- `$regex` : Expression régulière (support dépendant du driver SQL).
- `$where` : Expression SQL brute (ex: `{ $where: "age > 18 AND age < 60" }`).
- `$comment` : Ajoute un commentaire dans les logs de requête (sans effet sur le résultat).

#### Opérateurs de tableau (Adaptés pour SQL)
*Note: Ces opérateurs s'appliquent aux champs de type `array` (stockés en JSON) ou aux chaînes séparées par des virgules.*
- `$all` : Doit contenir toutes les valeurs spécifiées (via recherche `LIKE`).
- `$size` : Vérifie le nombre d'éléments dans la liste/tableau.

#### Opérateurs géospatiaux (via JSON)
Requêtes sur des champs contenant des coordonnées au format `[longitude, latitude]`.
- `$geoWithin` : Trouve les points dans une zone géographique.
    - `$box` : `{ location: { $geoWithin: { $box: [[x1, y1], [x2, y2]] } } }`.
    - `$centerSphere` : `{ location: { $geoWithin: { $centerSphere: [[x, y], radius] } } }` (rayon en degrés décimaux).
- `$nearSphere` : Trouve les points proches d'une coordonnée et trie par distance. Supporte `{ $geometry, $maxDistance }`.
- `$geoIntersects` : Vérifie l'intersection avec un Point donné.

## Documents

Les instances de modèles représentent des documents synchronisés en temps réel avec le moteur interne.

```javascript
const user = await User.findOne({ name: 'Alice' });

// Synchronisation immédiate entre propriétés et méthodes
user.name = 'Alice Smith'; 
user.set('age', 31); 

// Sauvegarde et Suppression
await user.save();       // Via l'instance
await User.save(user);   // Via la méthode statique (équivalent)

await user.remove();     // Via l'instance
await User.remove(user); // Via la méthode statique (équivalent)
```

## Architecture technique

Le module repose sur **GORM** pour l'abstraction SQL et un moteur de script pour l'exécution JavaScript.

- `module.go` : Point d'entrée et enregistrement dans le moteur.
- `register.go` : Proxies Mongoose-like (Model, Schema) et pont natif/JS.
- `schema.go` : Définitions et types de données.
- `model.go` : Mappage vers les tables SQL et logique CRUD.
- `document.go` : Proxies d'instances de documents avec Getters/Setters synchronisés.
- `query.go` : Moteur de requêtes récursif avec support des opérateurs `$`.
- `connect.go` : Gestionnaire de connexions et support multi-driver.
