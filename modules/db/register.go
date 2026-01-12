package db

import (
	"fmt"

	"github.com/dop251/goja"
	"gorm.io/gorm"
)

func RegisterMongoose(vm *goja.Runtime, db *gorm.DB) goja.Value {
	conn := &Connection{
		db:      db,
		vm:      vm,
		models:  make(map[string]*Model),
		schemas: make(map[string]*Schema),
	}

	// Auto-migrate la table de schémas
	db.AutoMigrate(&SchemaRecord{})

	// Constructeur Schema
	schemaCtor := func(paths map[string]interface{}) goja.Value {
		schema := conn.createSchemaFromMap(paths)
		schema.vm = vm

		return createSchemaProxy(vm, schema)
	}

	dbObj := vm.NewObject()
	dbObj.Set("Schema", schemaCtor)
	dbObj.Set("Model", func(name string, schema goja.Value) goja.Value {
		model := conn.Model(name, schema)
		return createModelProxy(vm, model)
	})
	dbObj.Set("model", func(name string, schema goja.Value) goja.Value {
		model := conn.Model(name, schema)
		return createModelProxy(vm, model)
	})

	return dbObj
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

func createModelProxy(vm *goja.Runtime, model *Model) goja.Value {
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
		return q.ToJSObject()
	})

	obj.Set("find", func(filter map[string]any) goja.Value {
		q := NewQuery(model, vm)
		if filter != nil {
			q.Filter(filter)
		}
		return q.ToJSObject()
	})

	// Ajouter les fonctions statiques
	for funcName, funcCode := range model.Schema.Statics {
		fullCode := fmt.Sprintf("(%s)", funcCode)
		if fn, err := vm.RunString(fullCode); err == nil {
			obj.Set(funcName, fn)
		}
	}

	return obj
}
