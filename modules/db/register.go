package db

import (
	"fmt"
	"http-server/plugins/js"

	"github.com/dop251/goja"
	"gorm.io/gorm"
)

func RegisterMongoose(vm *goja.Runtime, db *gorm.DB, name ...string) goja.Value {
	var conn *Connection
	conn = NewConnection(db, name...)
	conn.vm = vm
	return conn.ToJSObject(vm)
}

func createSchemaProxy(vm *goja.Runtime, s *Schema) goja.Value {
	obj := vm.NewObject()

	obj.Set("virtual", func(name string) goja.Value {
		v := VirtualDef{}
		builder := vm.NewObject()
		builder.Set("get", func(fn goja.Value) goja.Value {
			v.Get = fn.String()
			s.Virtuals[name] = v
			return builder
		})
		builder.Set("set", func(fn goja.Value) goja.Value {
			v.Set = fn.String()
			s.Virtuals[name] = v
			return builder
		})
		return builder
	})

	obj.Set("pre", func(action string, fn goja.Value) goja.Value {
		s.Middleware[action] = append(s.Middleware[action], MiddlewareDef{
			Type:   "pre",
			Action: action,
			Fn:     fn.String(),
		})
		return obj
	})

	obj.Set("post", func(action string, fn goja.Value) goja.Value {
		s.Middleware[action] = append(s.Middleware[action], MiddlewareDef{
			Type:   "post",
			Action: action,
			Fn:     fn.String(),
		})
		return obj
	})

	// Accès direct aux maps pour compatibilité
	methods := vm.NewObject()
	obj.Set("methods", methods)
	statics := vm.NewObject()
	obj.Set("statics", statics)

	// Lien interne pour extraction
	obj.Set("__internal", s)

	return obj
}

func (conn *Connection) ToJSObject(vm *goja.Runtime) goja.Value {
	// Auto-migrate la table de schémas
	conn.db.AutoMigrate(&SchemaRecord{})

	// Constructeur Schema
	schemaCtor := func(paths map[string]interface{}) goja.Value {
		schema := conn.createSchemaFromMap(paths)
		schema.vm = vm

		return createSchemaProxy(vm, schema)
	}
	model := func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			vm.Interrupt(vm.NewGoError(fmt.Errorf("model requires at least a name")))
			return goja.Undefined()
		}
		name := call.Argument(0).String()
		var schema []any
		if len(call.Arguments) > 1 {
			schema = append(schema, call.Argument(1))
		}
		model := conn.Model(name, schema...)
		return conn.createModelProxy(vm, model)
	}
	dbObj := vm.NewObject()
	dbObj.Set("Schema", schemaCtor)
	dbObj.Set("Model", model)
	dbObj.Set("model", model)
	return dbObj
}
func (conn *Connection) createModelProxy(vm *goja.Runtime, model *Model) goja.Value {
	if model == nil {
		return goja.Undefined()
	}

	obj := vm.NewObject()

	// Méthode schema (comme mongoose.Schema)
	obj.Set("schema", func(schemaDef map[string]any) goja.Value {
		// Cette méthode est plutôt utilisée lors de la création du model
		return obj
	})

	// Méthodes du model
	obj.Set("method", func(name string, fnCode goja.Value) goja.Value {
		model.Method(name, fnCode.String())
		return obj
	})

	obj.Set("static", func(name string, fnCode goja.Value) goja.Value {
		model.Static(name, fnCode.String())
		return obj
	})

	// Opérations CRUD
	obj.Set("new", func(data map[string]any) goja.Value {
		doc := model.New(data)
		return doc.ToJSObject(vm)
	})

	obj.Set("create", func(data map[string]any) goja.Value {
		doc, err := model.Create(data)
		if err != nil {
			panic(vm.ToValue(err))
		}
		return doc.ToJSObject(vm)
	})

	obj.Set("findOne", func(filter map[string]any) goja.Value {
		q := NewQuery(model, vm)
		if filter != nil {
			q.Filter(filter)
		}
		q.Limit(1)
		return q.ToJSObject(true)
	})

	obj.Set("find", func(filter map[string]any) goja.Value {
		q := NewQuery(model, vm)
		if filter != nil {
			q.Filter(filter)
		}
		return q.ToJSObject()
	})

	// Static save/remove delegation
	obj.Set("save", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return goja.Undefined()
		}
		docObj := call.Argument(0).ToObject(vm)
		if saveFn, ok := goja.AssertFunction(docObj.Get("save")); ok {
			res, _ := saveFn(docObj)
			return res
		}
		return goja.Undefined()
	})

	obj.Set("remove", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return goja.Undefined()
		}
		docObj := call.Argument(0).ToObject(vm)
		if removeFn, ok := goja.AssertFunction(docObj.Get("remove")); ok {
			res, _ := removeFn(docObj)
			return res
		}
		return goja.Undefined()
	})

	// Ajouter les fonctions statiques
	for funcName, funcCode := range model.Schema.Statics {
		fullCode := js.GetFunction(funcCode)

		if fn, err := vm.RunString(fullCode); err == nil {
			obj.Set(funcName, func(call goja.FunctionCall) goja.Value {
				// Convert goja.Value to goja.Callable
				function, _ := goja.AssertFunction(fn)
				ret, e := function(obj, call.Arguments...)
				if e != nil {
					vm.Interrupt(e)
					return goja.Undefined()
				}
				return ret
			})
		} else {
			e := vm.NewGoError(err)
			obj.Set(funcName, func(call goja.FunctionCall) goja.Value {
				vm.Interrupt(e)
				return goja.Undefined()
			})
		}
	}

	return obj
}
