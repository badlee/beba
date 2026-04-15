package db

import (
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/dop251/goja"
)

type SchemaRecord struct {
	Name       string `gorm:"primaryKey"`
	Schema     string `gorm:"type:text"`
	Methods    string `gorm:"type:text"`
	Statics    string `gorm:"type:text"`
	Virtuals   string `gorm:"type:text"`
	Middleware string `gorm:"type:text"`
	Updated    time.Time
}

func (conn *Connection) Model(name string, schema ...interface{}) *Model {
	// Cas 1: Récupération d'un model existant (sans schéma)
	if len(schema) == 0 {
		conn.RLock()
		existingModel, exists := conn.models[name]
		if exists {
			conn.RUnlock()
			return existingModel
		}
		conn.RUnlock()

		// Restaurer depuis persistance
		restoredModel := conn.restoreModel(name)
		if restoredModel != nil {
			conn.Lock()
			conn.models[name] = restoredModel
			conn.Unlock()
			return restoredModel
		}

		return nil
	}

	// Cas 2: Création/Remplacement avec schéma
	return conn.model(name, schema[0], true)
}

func (conn *Connection) CreateModel(name string, schemaDef interface{}) *Model {
	return conn.model(name, schemaDef, false)
}

func (conn *Connection) model(name string, schemaDef interface{}, migrate bool) *Model {
	conn.Lock()
	defer conn.Unlock()

	var schemaObj *Schema

	// Conversion en objet JS si possible
	var jsObj *goja.Object
	if val, ok := schemaDef.(goja.Value); ok {
		jsObj = val.ToObject(conn.vm)
	} else if obj, ok := schemaDef.(*goja.Object); ok {
		jsObj = obj
	}

	if jsObj != nil {
		// Cas A: C'est un objet Schema avec un lien interne (via register.go)
		internal := jsObj.Get("__internal")
		if internal != nil && !goja.IsUndefined(internal) {
			if s, ok := internal.Export().(*Schema); ok {
				schemaObj = s
				// Sync methods/statics from JS proxy to Go struct
				if m := jsObj.Get("methods"); m != nil && !goja.IsUndefined(m) {
					if mObj := m.ToObject(conn.vm); mObj != nil {
						for _, k := range mObj.Keys() {
							schemaObj.Methods[k] = mObj.Get(k).String()
						}
					}
				}
				if st := jsObj.Get("statics"); st != nil && !goja.IsUndefined(st) {
					if stObj := st.ToObject(conn.vm); stObj != nil {
						for _, k := range stObj.Keys() {
							schemaObj.Statics[k] = stObj.Get(k).String()
						}
					}
				}
			}
		}

		// Cas B: C'est un objet JS simple (POJO)
		if schemaObj == nil {
			exported := jsObj.Export()
			if schemaMap, ok := exported.(map[string]interface{}); ok {
				schemaObj = conn.createSchemaFromMap(schemaMap)
			}
		}
	} else if s, ok := schemaDef.(*Schema); ok {
		// Cas C: C'est déjà un objet Schema Go
		schemaObj = s
	} else if schemaMap, ok := schemaDef.(map[string]interface{}); ok {
		// Cas D: C'est déjà une map Go
		schemaObj = conn.createSchemaFromMap(schemaMap)
	}

	if schemaObj == nil {
		return nil
	}

	conn.saveSchema(name, schemaObj)

	model := &Model{
		Name:   name,
		Schema: schemaObj,
		db:     conn.db,
		conn:   conn,
	}

	if migrate {
		t := model.createMigrationType()
		conn.db.Table(name).AutoMigrate(reflect.New(t).Interface())
	}

	conn.models[name] = model

	return model
}

func (conn *Connection) AutoMigrate() error {
	conn.RLock()
	defer conn.RUnlock()

	for name, m := range conn.models {
		t := m.createMigrationType()
		// Use .Table(name) to force the table name for unnamed structs
		if err := conn.db.Table(name).AutoMigrate(reflect.New(t).Interface()); err != nil {
			return err
		}
	}

	return nil
}

// Méthode pour restaurer un model complet
func (conn *Connection) restoreModel(name string) *Model {
	schema := conn.restoreSchema(name)
	if schema == nil {
		return nil
	}

	return &Model{
		Name:   name,
		Schema: schema,
		db:     conn.db,
		conn:   conn,
	}
}

func (conn *Connection) createSchemaFromMap(schemaMap map[string]interface{}) *Schema {
	paths := make(map[string]SchemaType)

	for fieldName, fieldDef := range schemaMap {
		schemaType := SchemaType{Type: "string"} // default

		if fieldDefMap, ok := fieldDef.(map[string]interface{}); ok {
			if typeVal, exists := fieldDefMap["type"]; exists {
				schemaType.Type = fmt.Sprintf("%v", typeVal)
			}
			if required, exists := fieldDefMap["required"]; exists {
				if reqBool, ok := required.(bool); ok {
					schemaType.Required = reqBool
				}
			}
			if defaultValue, exists := fieldDefMap["default"]; exists {
				schemaType.Default = defaultValue
			}
			if index, exists := fieldDefMap["index"]; exists {
				if idxBool, ok := index.(bool); ok {
					schemaType.Index = idxBool
				}
			}
			if unique, exists := fieldDefMap["unique"]; exists {
				if uniqBool, ok := unique.(bool); ok {
					schemaType.Unique = uniqBool
				}
			}
			if validate, exists := fieldDefMap["validate"]; exists {
				if validateStr, ok := validate.(string); ok {
					schemaType.Validate = validateStr
				}
			}
			if ref, exists := fieldDefMap["ref"]; exists {
				if refStr, ok := ref.(string); ok {
					schemaType.Ref = refStr
				}
			}
			if has, exists := fieldDefMap["has"]; exists {
				if hasStr, ok := has.(string); ok {
					schemaType.Has = hasStr
				}
			}
			if onDelete, exists := fieldDefMap["delete"]; exists {
				if delStr, ok := onDelete.(string); ok {
					schemaType.OnDelete = delStr
				}
			}
			if onUpdate, exists := fieldDefMap["update"]; exists {
				if updStr, ok := onUpdate.(string); ok {
					schemaType.OnUpdate = updStr
				}
			}
		} else if typeStr, ok := fieldDef.(string); ok {
			schemaType.Type = typeStr
		}

		paths[fieldName] = schemaType
	}

	return &Schema{
		Paths:      paths,
		Methods:    make(map[string]string),
		Statics:    make(map[string]string),
		Virtuals:   make(map[string]VirtualDef),
		Middleware: make(map[string][]MiddlewareDef),
	}
}

func (conn *Connection) restoreSchema(name string) *Schema {
	var record SchemaRecord
	if err := conn.db.Where("name = ?", name).First(&record).Error; err != nil {
		return nil
	}

	var schema Schema
	json.Unmarshal([]byte(record.Schema), &schema)
	schema.Methods = make(map[string]string)
	schema.Statics = make(map[string]string)
	schema.Virtuals = make(map[string]VirtualDef)
	schema.Middleware = make(map[string][]MiddlewareDef)

	// Restaurer components
	if record.Methods != "" {
		json.Unmarshal([]byte(record.Methods), &schema.Methods)
	}
	if record.Statics != "" {
		json.Unmarshal([]byte(record.Statics), &schema.Statics)
	}
	if record.Virtuals != "" {
		json.Unmarshal([]byte(record.Virtuals), &schema.Virtuals)
	}
	if record.Middleware != "" {
		json.Unmarshal([]byte(record.Middleware), &schema.Middleware)
	}

	return &schema
}

func (conn *Connection) saveSchema(name string, schema *Schema) error {
	schemaJSON, _ := json.Marshal(schema.Paths)
	methodsJSON, _ := json.Marshal(schema.Methods)
	staticsJSON, _ := json.Marshal(schema.Statics)
	virtualsJSON, _ := json.Marshal(schema.Virtuals)
	middlewareJSON, _ := json.Marshal(schema.Middleware)

	record := SchemaRecord{
		Name:       name,
		Schema:     string(schemaJSON),
		Methods:    string(methodsJSON),
		Statics:    string(staticsJSON),
		Virtuals:   string(virtualsJSON),
		Middleware: string(middlewareJSON),
		Updated:    time.Now(),
	}

	return conn.db.Where("name = ?", name).Assign(record).FirstOrCreate(&record).Error
}
