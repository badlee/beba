package db

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/dop251/goja"
)

func (d *Document) Get(path string) interface{} {
	return d.Data[path]
}

func (d *Document) Set(path string, value interface{}) {
	d.Data[path] = value
}

func (d *Document) Save() error {
	// Pre-save hooks
	d.Model.runHooks("pre", "save", d)

	structType := d.Model.createStructType()
	instance := reflect.New(structType).Elem()

	// Remplir l'instance avec les données
	for fieldName, value := range d.Data {
		goFieldName := toCamelCase(fieldName)
		field := instance.FieldByName(goFieldName)
		if field.IsValid() && field.CanSet() {
			// Handle array serialization
			if d.Model.Schema.Paths[fieldName].Type == "array" {
				if value != nil {
					jsonBytes, _ := json.Marshal(value)
					field.SetString(string(jsonBytes))
				}
				continue
			}

			fieldValue := reflect.ValueOf(value)
			if fieldValue.IsValid() && fieldValue.Type().ConvertibleTo(field.Type()) {
				field.Set(fieldValue.Convert(field.Type()))
			}
		}
	}

	// Définir l'ID
	idField := instance.FieldByName("ID")
	if idField.IsValid() && idField.CanSet() {
		idField.SetString(d.ID)
	}

	instancePtr := instance.Addr().Interface()

	var err error
	if d.isNew {
		err = d.Model.db.Table(d.Model.Name).Create(instancePtr).Error
		d.isNew = false
	} else {
		err = d.Model.db.Table(d.Model.Name).Save(instancePtr).Error
	}

	// Post-save hooks
	if err == nil {
		d.Model.runHooks("post", "save", d)
	}

	return err
}

func (d *Document) Remove() error {
	structType := d.Model.createStructType()
	instance := reflect.New(structType).Elem()

	// Définir l'ID
	idField := instance.FieldByName("ID")
	if idField.IsValid() && idField.CanSet() {
		idField.SetString(d.ID)
	}

	return d.Model.db.Table(d.Model.Name).Delete(instance.Addr().Interface()).Error
}

func (d *Document) ToObject() map[string]interface{} {
	return d.Data
}

// ToJSObject - Créer un proxy JavaScript pour le document
func (d *Document) ToJSObject(vm *goja.Runtime) goja.Value {
	obj := vm.NewObject()

	// 1. Données du document
	for key, value := range d.Data {
		obj.Set(key, value)
	}

	// 2. ID et meta
	obj.Set("_id", d.ID)
	obj.Set("id", d.ID)

	// 3. Méthodes du schéma
	for methodName, methodCode := range d.Model.Schema.Methods {
		fullCode := fmt.Sprintf("(function() { return %s; })()", methodCode)
		if fn, err := vm.RunString(fullCode); err == nil {
			obj.Set(methodName, fn)
		}
	}

	// 4. Virtuals (Getters / Setters)
	for virtualName, virtualDef := range d.Model.Schema.Virtuals {
		// On utilise Object.defineProperty pour les virtuals
		defineProperty, _ := goja.AssertFunction(vm.Get("Object").ToObject(vm).Get("defineProperty"))

		desc := vm.NewObject()
		desc.Set("enumerable", true)
		desc.Set("configurable", true)

		if virtualDef.Get != "" {
			getFn, _ := vm.RunString(fmt.Sprintf("(function() { return %s; })()", virtualDef.Get))
			desc.Set("get", getFn)
		}
		if virtualDef.Set != "" {
			setFn, _ := vm.RunString(fmt.Sprintf("(function() { return %s; })()", virtualDef.Set))
			desc.Set("set", setFn)
		}

		defineProperty(goja.Undefined(), obj, vm.ToValue(virtualName), desc)
	}

	// 5. Méthodes de base (save, remove, etc.)
	obj.Set("save", func(call goja.FunctionCall) goja.Value {
		// Update data from proxy before saving
		// In a real proxy we would intercept sets, but here we just sync top-level
		for key := range d.Model.Schema.Paths {
			d.Data[key] = obj.Get(key).Export()
		}

		err := d.Save()
		if err != nil {
			panic(vm.ToValue(err))
		}
		return vm.ToValue(obj) // Return self for chaining
	})

	obj.Set("remove", func(call goja.FunctionCall) goja.Value {
		err := d.Remove()
		if err != nil {
			panic(vm.ToValue(err))
		}
		return goja.Undefined()
	})

	obj.Set("get", func(path string) goja.Value {
		return vm.ToValue(d.Get(path))
	})

	obj.Set("set", func(path string, value goja.Value) goja.Value {
		d.Set(path, value.Export())
		obj.Set(path, value)
		return goja.Undefined()
	})

	obj.Set("toObject", func(call goja.FunctionCall) goja.Value {
		// Sync again
		res := make(map[string]interface{})
		for key := range d.Model.Schema.Paths {
			res[key] = obj.Get(key).Export()
		}
		return vm.ToValue(res)
	})

	return obj
}

func toJSONString(data map[string]interface{}) string {
	jsonBytes, _ := json.Marshal(data)
	return string(jsonBytes)
}
