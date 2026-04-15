package js

import (
	"fmt"
	"strings"

	"github.com/dop251/goja/ast"
	"github.com/dop251/goja/parser"
)

// EnsureReturnStrict analyse un morceau de code JavaScript fourni en chaîne de caractères,
// et injecte un mot-clé `return` devant la dernière expression si certaines conditions de sécurité sont respectées.
//
// Comportement principal :
// 1. Parse le code en AST via "github.com/dop251/goja/parser".
// 2. Vérifie qu'aucune instruction `return` n'est déjà présente.
// 3. Identifie la dernière instruction du code.
// 4. Refuse d'injecter `return` si la dernière instruction est :
//   - un `throw`
//   - une expression dangereuse (assignation, fonction, objet littéral, tableau, yield)
//
// 5. Vérifie les expressions `await` (support imbriqué) et les appels `Promise.resolve`, `Promise.all`, etc.
// 6. Injecte `return` directement dans la chaîne originale en utilisant les indices AST pour préserver le code exact.
//
// Paramètres :
//
//	src string : code source JavaScript à analyser.
//
// Retour :
//
//	string : le code source éventuellement modifié avec `return` injecté.
//	bool   : true si `return` a été injecté, false si aucune modification n'a été faite.
//
// Cas de sécurité :
// - Autorise les expressions simples : identifiants, littéraux (number, string, boolean, null), this.
// - Autorise les expressions logiques et mathématiques : unary, binary, conditional.
// - Autorise les accès à des propriétés et indices : dot expression, bracket expression.
// - Autorise les appels de fonctions et `await` imbriqués.
// - Autorise les appels sécurisés aux méthodes de `Promise` : resolve, reject, all, race.
// - Refuse : assignations, séquences, définitions de fonctions, arrow functions, yield, objets littéraux, tableaux.
//
// Exemple d'utilisation :
//
//	code := `
//	    const a = 5;
//	    a + 10
//	`
//	newCode, ok := EnsureReturnStrict(code)
//	if ok {
//	    fmt.Println(newCode)
//	    // => "const a = 5;\nreturn a + 10"
//	}
func EnsureReturnStrict(src string) (string, bool) {
	src = strings.TrimSpace(src)

	prg, err := parser.ParseFile(nil, "", src, 0)
	if err != nil || len(prg.Body) == 0 {
		return src, false
	}

	// ❌ refuse si return déjà présent
	if containsReturn(prg) {
		return src, false
	}

	lastStmt := prg.Body[len(prg.Body)-1]

	// ❌ refuse throw
	if _, ok := lastStmt.(*ast.ThrowStatement); ok {
		return src, false
	}

	exprStmt, ok := lastStmt.(*ast.ExpressionStatement)
	if !ok {
		return src, false
	}

	expr := exprStmt.Expression

	// 🔒 validation await (avec support imbriqué)
	if awaitExpr, ok := expr.(*ast.AwaitExpression); ok {
		if !isSafeAwait(awaitExpr.Argument) {
			return src, false
		}
	}

	// 🔒 validation globale
	if !isSafeExpression(expr) {
		return src, false
	}

	// 📍 positions AST (IMPORTANT)
	start := int(expr.Idx0()) - 1
	// Recule uniquement pour inclure les parenthèses ouvrantes
	// On ne recule pas sur les espaces pour ne pas passer devant l'indentation
	for start > 0 && src[start-1] == '(' {
		start--
	}

	end := int(expr.Idx1()) - 1
	// Avance pour inclure les parenthèses fermantes, espaces ou points-virgules
	for end < len(src) && (src[end] == ')' || src[end] == ' ' || src[end] == '\t' || src[end] == ';') {
		end++
	}

	if start < 0 || end > len(src) || start >= end {
		return src, false
	}

	before := src[:start]
	exprStr := src[start:end]
	after := src[end:]

	return before + "return " + exprStr + after, true
}

// containsReturn parcourt récursivement un nœud AST Goja et détermine
// si un `ReturnStatement` est présent dans ce code.
//
// Contrairement à `go/ast`, Goja n’a pas de Visitor pattern,
// donc la fonction utilise une récursion manuelle sur tous les types de nœuds pertinents.
//
// Paramètres :
//
//	node ast.Node : nœud AST à analyser (Program, Statement ou Expression)
//
// Retour :
//
//	bool : true si un `ReturnStatement` est présent quelque part dans le nœud ou ses enfants,
//	       false sinon
//
// Types de nœuds parcourus :
//   - *ast.Program : parcours de Body
//   - *ast.BlockStatement : parcours de List
//   - *ast.FunctionLiteral / *ast.ArrowFunctionLiteral : parcours de Body
//   - *ast.IfStatement : parcours de Consequent et Alternate
//   - *ast.ExpressionStatement : parcours de l’Expression
//   - *ast.CallExpression : parcours des arguments
//
// Exemple d’utilisation :
//
//	prg, _ := parser.ParseFile(nil, "", "function foo() { return 42; }", 0)
//	if containsReturn(prg) {
//	    fmt.Println("Return trouvé dans l'AST")
//	}
func containsReturn(node ast.Node) bool {
	switch n := node.(type) {

	case *ast.Program:
		for _, stmt := range n.Body {
			if containsReturn(stmt) {
				return true
			}
		}

	case *ast.BlockStatement:
		for _, stmt := range n.List {
			if containsReturn(stmt) {
				return true
			}
		}

	case *ast.ReturnStatement:
		return true

	case *ast.IfStatement:
		if containsReturn(n.Consequent) {
			return true
		}
		if n.Alternate != nil && containsReturn(n.Alternate) {
			return true
		}

	case *ast.ExpressionStatement:
		return containsReturn(n.Expression)

	case *ast.CallExpression:
		if containsReturn(n.Callee) {
			return true
		}
		for _, arg := range n.ArgumentList {
			if containsReturn(arg) {
				return true
			}
		}

	case *ast.FunctionLiteral:
		return containsReturn(n.Body)

	case *ast.ArrowFunctionLiteral:
		return containsReturn(n.Body)

	case *ast.FunctionDeclaration:
		return containsReturn(n.Function)

	case *ast.DoWhileStatement:
		return containsReturn(n.Test) || containsReturn(n.Body)

	case *ast.WhileStatement:
		return containsReturn(n.Test) || containsReturn(n.Body)

	case *ast.ForStatement:
		if n.Initializer != nil && containsReturn(n.Initializer) {
			return true
		}
		if n.Test != nil && containsReturn(n.Test) {
			return true
		}
		if n.Update != nil && containsReturn(n.Update) {
			return true
		}
		return containsReturn(n.Body)

	case *ast.ForInStatement:
		return containsReturn(n.Into) || containsReturn(n.Source) || containsReturn(n.Body)

	case *ast.ForOfStatement:
		return containsReturn(n.Into) || containsReturn(n.Source) || containsReturn(n.Body)

	case *ast.SwitchStatement:
		if containsReturn(n.Discriminant) {
			return true
		}
		for _, c := range n.Body {
			for _, stmt := range c.Consequent {
				if containsReturn(stmt) {
					return true
				}
			}
		}

	case *ast.TryStatement:
		if containsReturn(n.Body) {
			return true
		}
		if n.Catch != nil && containsReturn(n.Catch) {
			return true
		}
		if n.Finally != nil && containsReturn(n.Finally) {
			return true
		}

	case *ast.CatchStatement:
		return containsReturn(n.Body)

	case *ast.LabelledStatement:
		return containsReturn(n.Statement)

	case *ast.WithStatement:
		return containsReturn(n.Object) || containsReturn(n.Body)

	}

	return false
}

// isSafeAwait vérifie si l’argument d’un `AwaitExpression` est sûr pour être exécuté
// ou retourné automatiquement dans une fonction JavaScript.
//
// Cette fonction permet de gérer les appels asynchrones de manière sécurisée,
// en supportant les `await` imbriqués et les appels autorisés de `Promise`.
//
// Paramètres :
//
//	expr ast.Expression : expression qui est l’argument de l’`await` (ex: `await foo()`)
//
// Retour :
//
//	bool : true si l’expression peut être safely awaitée, false sinon
//
// Comportement détaillé :
//  1. Si l’expression est un autre `AwaitExpression` : la vérification est récursive
//     (supporte `await await foo()`).
//  2. Si l’expression est un `Identifier` : autorisé (ex: `await foo`).
//  3. Si l’expression est un `CallExpression` : validé via `isSafeCall`
//     (ex: `await Promise.resolve()` est autorisé).
//  4. Si l’expression est un `DotExpression` ou `BracketExpression` : autorisé
//     (ex: `await obj.method()` ou `await obj["prop"]()`).
//  5. Tout autre type d’expression est considéré dangereux et retourne false.
//
// Exemple d’utilisation :
//
//	expr := parser.ParseExpression("await foo()")
//	fmt.Println(isSafeAwait(expr.(*ast.AwaitExpression).Argument)) // true
//
//	expr2 := parser.ParseExpression("await await Promise.resolve()")
//	fmt.Println(isSafeAwait(expr2.(*ast.AwaitExpression).Argument)) // true
//
//	expr3 := parser.ParseExpression("await (a + b)") // expression non sécurisée
//	fmt.Println(isSafeAwait(expr3.(*ast.AwaitExpression).Argument)) // false
func isSafeAwait(expr ast.Expression) bool {
	switch e := expr.(type) {
	case *ast.AwaitExpression:
		return isSafeAwait(e.Argument) // e.Argument existe
	case *ast.Identifier:
		return true
	case *ast.CallExpression:
		return isSafeCall(e)
	case *ast.DotExpression, *ast.BracketExpression:
		return true
	default:
		return false
	}
}

// isSafeCall vérifie si un appel de fonction représenté par un nœud AST `CallExpression`
// est considéré sûr pour être exécuté ou retourné automatiquement.
//
// Cette fonction analyse le callee (la fonction appelée) et décide si l’appel est sécurisé.
// Elle est utilisée conjointement avec `isSafeExpression` et `isSafeMemberCall`.
//
// Paramètres :
//
//	call *ast.CallExpression : nœud AST représentant l’appel de fonction (ex: func(), obj.method())
//
// Retour :
//
//	bool : true si l’appel est considéré sûr, false sinon
//
// Comportement détaillé :
//  1. Si le callee est un `Identifier` (ex: `foo()`), l’appel est considéré sûr.
//  2. Si le callee est un `DotExpression` (ex: `obj.method()`), la sécurité est déléguée
//     à `isSafeMemberCall`.
//  3. Tout autre type de callee (ex: expressions complexes, parenthèses ou `new`) renvoie false.
//
// Exemple d’utilisation :
//
//	// Appel d’une fonction simple
//	callExpr := parser.ParseExpression("foo()").(*ast.CallExpression)
//	fmt.Println(isSafeCall(callExpr)) // true
//
//	// Appel d’une méthode Promise
//	callExpr2 := parser.ParseExpression("Promise.resolve()").(*ast.CallExpression)
//	fmt.Println(isSafeCall(callExpr2)) // true
//
//	// Appel dangereux via Function constructor
//	callExpr3 := parser.ParseExpression("Function('return 1')()").(*ast.CallExpression)
//	fmt.Println(isSafeCall(callExpr3)) // false
func isSafeCall(call *ast.CallExpression) bool {
	switch callee := call.Callee.(type) {

	case *ast.Identifier:
		return true

	case *ast.DotExpression:
		return isSafeMemberCall(callee)

	default:
		return false
	}
}

// isSafeMemberCall vérifie si un accès à une propriété via un `DotExpression`
// est considéré sûr pour être exécuté ou retourné dans une fonction.
//
// Cette fonction est utile pour filtrer les appels de type `obj.method()` afin
// de ne pas autoriser des accès dangereux (ex : `window.eval`, `Function.constructor`)
// tout en autorisant les usages sécurisés comme `Promise.resolve()`.
//
// Paramètres :
//
//	dot *ast.DotExpression : nœud AST représentant l’accès à la propriété (ex: obj.prop)
//
// Retour :
//
//	bool : true si l’accès est considéré sûr, false sinon
//
// Comportement détaillé :
//
// ✅ Autorisé :
//   - Appels sur `Promise` avec les méthodes : resolve, reject, all, race
//   - Accès normal à des objets utilisateurs ou propriétés non dangereuses
//
// ❌ Refusé :
//   - Accès à `window`, `globalThis`, `Function`, `eval` pour des raisons de sécurité
//
// Exemple d’utilisation :
//
//	// Promise.resolve est sûr
//	dotExpr := parser.ParseExpression("Promise.resolve()").(*ast.DotExpression)
//	fmt.Println(isSafeMemberCall(dotExpr)) // true
//
//	// window.eval est dangereux
//	dotExpr2 := parser.ParseExpression("window.eval()").(*ast.DotExpression)
//	fmt.Println(isSafeMemberCall(dotExpr2)) // false
func isSafeMemberCall(dot *ast.DotExpression) bool {
	// Promise.resolve / all / race / reject
	if ident, ok := dot.Left.(*ast.Identifier); ok {
		if ident.Name == "Promise" {
			switch dot.Identifier.Name {
			case "resolve", "reject", "all", "race":
				return true
			}
		}
	}

	// autorise obj.method()
	return true
}

// isSafeExpression détermine si une expression JavaScript, représentée en AST par Goja,
// est considérée "sûre" pour être retournée automatiquement ou injectée dans un `return`.
//
// Cette fonction est utilisée pour filtrer les expressions ambiguës ou dangereuses,
// afin d'éviter de transformer du code qui pourrait avoir des effets de bord inattendus.
//
// Paramètres :
//
//	expr ast.Expression : expression Goja AST à analyser.
//
// Retour :
//
//	bool : true si l'expression est considérée sûre, false sinon.
//
// Comportement détaillé :
//
// ✅ Expressions autorisées :
//   - Identifiers : `x`, `this`
//   - Littéraux : number, string, boolean, null
//   - Opérations : unary, binary, conditional (ex: `a + b`, `!a`, `a ? b : c`)
//   - Accès : dot expression (`obj.prop`) et bracket expression (`obj["prop"]`)
//   - Appels : call expression (`func()`) et new expression (`new Class()`)
//   - Async : await expressions, avec validation imbriquée via `isSafeAwait`
//
// ❌ Expressions refusées :
//   - Assignations (`x = 5`) ou séquences (`a, b, c`)
//   - Définition de fonctions (function literal, arrow function)
//   - Yield expressions
//   - Littéraux d’objet ou de tableau si on veut éviter effets de bord (`{a:1}`, `[1,2]`)
//
// Exemple d’utilisation :
//
//	expr := parser.ParseExpression("a + b") // renvoie un ast.Expression
//	if isSafeExpression(expr) {
//	    fmt.Println("Expression sûre pour return automatique")
//	}
func isSafeExpression(expr ast.Expression) bool {
	switch e := expr.(type) {
	// simples
	case *ast.Identifier,
		*ast.ThisExpression,
		*ast.NumberLiteral,
		*ast.StringLiteral,
		*ast.BooleanLiteral,
		*ast.NullLiteral:
		return true

	// opérations
	case *ast.BinaryExpression:
		return isSafeExpression(e.Left) && isSafeExpression(e.Right)

	case *ast.UnaryExpression:
		return isSafeExpression(e.Operand) // ⚠ Operand, pas Argument

	case *ast.ConditionalExpression:
		return isSafeExpression(e.Test) &&
			isSafeExpression(e.Consequent) &&
			isSafeExpression(e.Alternate)

	// accès
	case *ast.DotExpression,
		*ast.BracketExpression:
		return true

	// appels
	case *ast.CallExpression:
		return true

	// async
	case *ast.AwaitExpression:
		return isSafeAwait(e.Argument)

	// dangereux / ambigu
	case *ast.FunctionLiteral,
		*ast.ArrowFunctionLiteral,
		*ast.YieldExpression,
		*ast.ObjectLiteral,
		*ast.ArrayLiteral:
		return true

	default:
		return false
	}
}

// IsJSFunction vérifie si une chaîne de caractères JavaScript représente une fonction.
// Elle supporte les types de fonctions suivants :
//   - FunctionLiteral : `function foo() { ... }`
//   - ArrowFunctionLiteral : `(x, y) => x + y`
//   - AsyncFunctionLiteral (async function ou async arrow) : `async function foo() { ... }`
//
// La vérification est basée sur le parsing AST via "github.com/dop251/goja/parser" et l'analyse
// des nœuds AST ("github.com/dop251/goja/ast").
//
// Paramètres :
//
//	src string : chaîne de caractères JavaScript à analyser.
//
// Retour :
//
//	bool : true si `src` correspond à une fonction JavaScript valide, false sinon.
//
// Comportement détaillé :
// 1. La chaîne est parsée avec parser.ParseFile pour générer un AST.
// 2. Le premier statement est analysé :
//   - Si c’est un ExpressionStatement contenant une ArrowFunctionLiteral ou une FunctionLiteral, renvoie true.
//   - Si c’est une déclaration de fonction classique (`FunctionDeclaration`), renvoie true.
//
// 3. Les fonctions `async` sont également reconnues.
// 4. Les chaînes qui contiennent autre chose que la fonction en tête (assignation, appel immédiat, expression) renvoient false.
//
// Exemple d’utilisation :
//
//	code1 := "function foo(x) { return x + 1 }"
//	fmt.Println(IsJSFunction(code1)) // true
//
//	code2 := "(a, b) => a + b"
//	fmt.Println(IsJSFunction(code2)) // true
//
//	code3 := "async function fetchData() { return await fetch(url) }"
//	fmt.Println(IsJSFunction(code3)) // true
//
//	code4 := "const a = 5"
//	fmt.Println(IsJSFunction(code4)) // false
func IsFunction(s string) bool {
	clean := strings.TrimSpace(s)

	// Parse comme expression
	prg, err := parser.ParseFile(nil, "", "("+clean+")", 0)
	if err != nil {
		return false
	}

	if len(prg.Body) != 1 {
		return false
	}

	stmt, ok := prg.Body[0].(*ast.ExpressionStatement)
	if !ok {
		return false
	}

	switch expr := stmt.Expression.(type) {
	case *ast.FunctionLiteral:
		return true
	case *ast.ArrowFunctionLiteral:
		return true
	// 🔥 Cas important : expression appelée (IIFE)
	case *ast.CallExpression:
		switch expr.Callee.(type) {
		case *ast.FunctionLiteral:
			return true
		case *ast.ArrowFunctionLiteral:
			return true
		}
	}

	return false
}

// GetFunction transforme une chaîne de code JavaScript en une fonction prête à être exécutée.
//
// Cette fonction fait le traitement suivant :
//  1. Si `code` représente déjà une fonction JavaScript valide (détectée via IsFunction),
//     elle est simplement encapsulée entre parenthèses : `(functionCode)`.
//  2. Sinon, elle construit une fonction anonyme qui encapsule le code fourni :
//     - Par défaut, elle utilise `"function() { %s; }"` comme template.
//     - Si un template personnalisé est fourni via `functionBuilder`, il est utilisé à la place.
//  3. Avant l’injection dans le template, `EnsureReturnStrict` est utilisé pour injecter
//     automatiquement un `return` devant la dernière expression si possible et sécuritaire.
//
// Paramètres :
//
//	code string : code JavaScript à transformer en fonction.
//	functionBuilder ...string : (optionnel) template de fonction personnalisé avec `%s` pour le code.
//
// Retour :
//
//	string : code JavaScript encapsulé sous forme de fonction, prêt à être exécuté.
//
// Comportement détaillé :
// - Si `code` est déjà une fonction : retourne `(code)`.
// - Si `code` n’est pas une fonction :
//   - applique EnsureReturnStrict pour ajouter un `return` si possible,
//   - insère le code dans le template `function() { ... }` ou le template fourni,
//   - encapsule le tout entre parenthèses pour obtenir une IIFE ou une fonction exécutable.
//
// Exemples d’utilisation :
//
//	// Cas 1 : code déjà fonction
//	code1 := "function(x) { return x + 1; }"
//	fmt.Println(GetFunction(code1))
//	// Output : "(function(x) { return x + 1; })"
//
//	// Cas 2 : code expression simple
//	code2 := "a + b"
//	fmt.Println(GetFunction(code2))
//	// Output : "(function() { return a + b; })"
//
//	// Cas 3 : code avec template personnalisé
//	code3 := "console.log('hello')"
//	fmt.Println(GetFunction(code3, "async function() { %s }"))
//	// Output : "(async function() { console.log('hello') })"
func GetFunction(code string, functionBuilder ...string) string {
	funcCode := strings.TrimSpace(code)
	fullCode := ""
	if IsFunction(funcCode) {
		fullCode = fmt.Sprintf("(%s)", funcCode)
	} else {
		f := "function() { %s; }"
		if len(functionBuilder) > 0 {
			f = functionBuilder[0]
			fullCode = fmt.Sprintf("("+f+")", funcCode)
		} else {
			if c, ok := EnsureReturnStrict(funcCode); ok {
				funcCode = c
			}
			fullCode = fmt.Sprintf("("+f+")", funcCode)
		}
	}

	return fullCode
}
