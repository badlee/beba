package db

import (
	"encoding/json"
	"fmt"
	"http-server/plugins/js"
	"reflect"
	"strings"

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
	defineProperty, _ := goja.AssertFunction(vm.Get("Object").ToObject(vm).Get("defineProperty"))

	// 1. Définition des propriétés du schéma (Getters/Setters pour synchronisation immédiate)
	for key := range d.Model.Schema.Paths {
		k := key
		desc := vm.NewObject()
		desc.Set("enumerable", true)
		desc.Set("configurable", true)

		desc.Set("get", func(call goja.FunctionCall) goja.Value {
			return vm.ToValue(d.Get(k))
		})

		desc.Set("set", func(call goja.FunctionCall) goja.Value {
			val := call.Argument(0)
			d.Set(k, val.Export())
			return goja.Undefined()
		})

		defineProperty(goja.Undefined(), obj, vm.ToValue(k), desc)
	}

	// 2. ID et meta
	obj.Set("_id", d.ID)
	obj.Set("id", d.ID)

	// 3. Méthodes du schéma (bound au document via 'this')
	for methodName, methodCode := range d.Model.Schema.Methods {
		fullCode := js.GetFunction(methodCode)
		if fn, err := vm.RunString(fullCode); err == nil {
			obj.Set(methodName, fn)
		} else {
			e := vm.NewGoError(err)
			obj.Set(methodName, func(call goja.FunctionCall) goja.Value {
				vm.Interrupt(e)
				return goja.Undefined()
			})
		}

	}

	// 4. Virtuals (Getters / Setters) — bound au document via 'this'
	for virtualName, virtualDef := range d.Model.Schema.Virtuals {
		desc := vm.NewObject()
		desc.Set("enumerable", true)
		desc.Set("configurable", true)

		if virtualDef.Get != "" {
			getCode := strings.TrimSpace(virtualDef.Get)
			desc.Set("get", func(call goja.FunctionCall) goja.Value {
				fnVal, err := vm.RunString(js.GetFunction(getCode))
				if err != nil {
					return goja.Undefined()
				}
				fn, ok := goja.AssertFunction(fnVal)
				if !ok {
					return goja.Undefined()
				}
				result, _ := fn(obj)
				return result
			})
		}
		if virtualDef.Set != "" {
			setCode := virtualDef.Set
			desc.Set("set", func(call goja.FunctionCall) goja.Value {
				fullCode := fmt.Sprintf("(function(v) { %s })", setCode)
				fnVal, err := vm.RunString(fullCode)
				if err != nil {
					return goja.Undefined()
				}
				fn, ok := goja.AssertFunction(fnVal)
				if !ok {
					return goja.Undefined()
				}
				fn(obj, call.Argument(0))
				return goja.Undefined()
			})
		}

		defineProperty(goja.Undefined(), obj, vm.ToValue(virtualName), desc)
	}

	// 5. Méthodes de base (save, remove, etc.)
	obj.Set("save", func(call goja.FunctionCall) goja.Value {
		err := d.Save()
		if err != nil {
			panic(vm.ToValue(err))
		}
		return vm.ToValue(obj)
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
		return goja.Undefined()
	})

	obj.Set("toObject", func(call goja.FunctionCall) goja.Value {
		return vm.ToValue(d.Data)
	})

	return obj
}

func toJSONString(data map[string]interface{}) string {
	jsonBytes, _ := json.Marshal(data)
	return string(jsonBytes)
}
