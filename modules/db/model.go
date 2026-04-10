package db

import (
	"encoding/json"
	"fmt"
	"http-server/plugins/js"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/dop251/goja"
)

// Méthodes de Model
func (m *Model) Method(name string, fnCode string) *Model {
	m.Schema.Methods[name] = fnCode
	// Sauvegarder la mise à jour
	m.saveSchema()
	return m
}

func (m *Model) Static(name string, fnCode string) *Model {
	m.Schema.Statics[name] = fnCode
	// Sauvegarder la mise à jour
	m.saveSchema()
	return m
}

func (m *Model) saveSchema() {
	schemaJSON, _ := json.Marshal(m.Schema.Paths)
	methodsJSON, _ := json.Marshal(m.Schema.Methods)
	staticsJSON, _ := json.Marshal(m.Schema.Statics)
	virtualsJSON, _ := json.Marshal(m.Schema.Virtuals)
	middlewareJSON, _ := json.Marshal(m.Schema.Middleware)

	record := SchemaRecord{
		Name:       m.Name,
		Schema:     string(schemaJSON),
		Methods:    string(methodsJSON),
		Statics:    string(staticsJSON),
		Virtuals:   string(virtualsJSON),
		Middleware: string(middlewareJSON),
		Updated:    time.Now(),
	}

	m.db.Where("name = ?", m.Name).Assign(record).FirstOrCreate(&record)
}

// Opérations CRUD
func (m *Model) New(data map[string]interface{}) *Document {
	// Appliquer les valeurs par défaut et validations
	validatedData := m.validateAndDefault(data)

	doc := &Document{
		Data:  validatedData,
		Model: m,
		ID:    strconv.Itoa(int(time.Now().UnixNano())),
		isNew: true,
	}

	return doc
}

func (m *Model) Create(data map[string]interface{}) (*Document, error) {
	doc := m.New(data)

	err := doc.Save()
	if err != nil {
		return nil, err
	}

	return doc, nil
}

func (m *Model) runHooks(hookType, action string, doc *Document) {
	if m.Schema == nil || m.Schema.vm == nil {
		return
	}

	hooks := m.Schema.Middleware[action]
	for _, h := range hooks {
		if h.Type == hookType {
			// Créer un proxy pour le hook
			jsDoc := doc.ToJSObject(m.Schema.vm)

			// Exécuter le hook avec jsDoc comme 'this'
			hookCode := js.GetFunction(strings.TrimSpace(h.Fn), "function(next) { %s; if(typeof next === 'function') next(); }")
			fnVal, err := m.Schema.vm.RunString(hookCode)
			if err == nil {
				if fn, ok := goja.AssertFunction(fnVal); ok {
					_, err = fn(jsDoc)
					if err == nil {
						// Re-sync data after pre hook
						if hookType == "pre" {
							for key := range m.Schema.Paths {
								doc.Data[key] = jsDoc.ToObject(m.Schema.vm).Get(key).Export()
							}
						}
					}
				}
			}
		}
	}
}

func (m *Model) FindOne(filter map[string]interface{}) (*Document, error) {
	structType := m.createStructType()
	result := reflect.New(structType).Interface()

	query := m.db
	for key, value := range filter {
		query = query.Where(fmt.Sprintf("%s = ?", key), value)
	}

	if err := query.First(result).Error; err != nil {
		return nil, err
	}

	// Convertir en Document
	resultValue := reflect.ValueOf(result).Elem()
	data := make(map[string]interface{})

	for i := 0; i < resultValue.NumField(); i++ {
		field := resultValue.Field(i)
		fieldType := resultValue.Type().Field(i)

		jsonTag := string(fieldType.Tag.Get("json"))
		if jsonTag != "" {
			fieldName := strings.Split(jsonTag, ",")[0]
			if fieldName != "" && fieldName != "-" {
				val := field.Interface()
				// Handle array deserialization
				if m.Schema.Paths[fieldName].Type == "array" {
					if s, ok := val.(string); ok && s != "" {
						var arr []interface{}
						if err := json.Unmarshal([]byte(s), &arr); err == nil {
							val = arr
						}
					}
				}
				data[fieldName] = val
			}
		}
	}

	return &Document{
		Data:  data,
		Model: m,
		ID:    fmt.Sprintf("%v", data["id"]),
		isNew: false,
	}, nil
}

func (m *Model) Find(filter map[string]interface{}) ([]*Document, error) {
	structType := m.createStructType()

	query := m.db
	for key, value := range filter {
		query = query.Where(fmt.Sprintf("%s = ?", key), value)
	}

	// Obtenir tous les résultats
	sliceType := reflect.SliceOf(structType)
	slice := reflect.MakeSlice(sliceType, 0, 0)
	slicePtr := reflect.New(sliceType)
	slicePtr.Elem().Set(slice)

	if err := query.Find(slicePtr.Interface()).Error; err != nil {
		return nil, err
	}

	sliceValue := slicePtr.Elem()
	var docs []*Document

	for i := 0; i < sliceValue.Len(); i++ {
		elem := sliceValue.Index(i)
		data := make(map[string]interface{})

		for j := 0; j < elem.NumField(); j++ {
			field := elem.Field(j)
			fieldType := elem.Type().Field(j)

			jsonTag := string(fieldType.Tag.Get("json"))
			if jsonTag != "" {
				fieldName := strings.Split(jsonTag, ",")[0]
				if fieldName != "" && fieldName != "-" {
					val := field.Interface()
					// Handle array deserialization
					if m.Schema.Paths[fieldName].Type == "array" {
						if s, ok := val.(string); ok && s != "" {
							var arr []interface{}
							if err := json.Unmarshal([]byte(s), &arr); err == nil {
								val = arr
							}
						}
					}
					data[fieldName] = val
				}
			}
		}

		docs = append(docs, &Document{
			Data:  data,
			Model: m,
			ID:    fmt.Sprintf("%v", data["id"]),
			isNew: false,
		})
	}

	return docs, nil
}

func (m *Model) createStructType() reflect.Type {
	structFields := []reflect.StructField{
		{
			Name: "ID",
			Type: reflect.TypeOf(""),
			Tag:  `gorm:"primaryKey" json:"id"`,
		},
		{
			Name: "CreatedAt",
			Type: reflect.TypeOf(time.Time{}),
			Tag:  `gorm:"autoCreateTime" json:"created_at"`,
		},
		{
			Name: "UpdatedAt",
			Type: reflect.TypeOf(time.Time{}),
			Tag:  `gorm:"autoUpdateTime" json:"updated_at"`,
		},
	}

	for fieldName, fieldSchema := range m.Schema.Paths {
		goFieldName := toCamelCase(fieldName)
		goFieldType := m.getGoType(fieldSchema.Type)

		tag := fmt.Sprintf(`json:"%s" gorm:"column:%s"`, fieldName, fieldName)
		if fieldSchema.Index || fieldSchema.Unique {
			if fieldSchema.Unique {
				tag += ` gorm:"index:unique"`
			} else {
				tag += ` gorm:"index"`
			}
		}

		structFields = append(structFields, reflect.StructField{
			Name: goFieldName,
			Type: goFieldType,
			Tag:  reflect.StructTag(tag),
		})
	}

	return reflect.StructOf(structFields)
}

func toCamelCase(fieldName string) string {
	parts := strings.FieldsFunc(fieldName, func(r rune) bool {
		return r == '_' || r == '-' || r == ' '
	})

	for i, part := range parts {
		if part == "" {
			continue
		}
		runes := []rune(part)
		runes[0] = unicode.ToUpper(runes[0])
		for j := 1; j < len(runes); j++ {
			runes[j] = unicode.ToLower(runes[j])
		}
		parts[i] = string(runes)
	}

	return strings.Join(parts, "")
}

func (m *Model) validateAndDefault(data map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})

	// Appliquer les valeurs par défaut
	for fieldName, fieldSchema := range m.Schema.Paths {
		if _, exists := data[fieldName]; !exists {
			if fieldSchema.Default != nil {
				result[fieldName] = fieldSchema.Default
			}
		} else {
			result[fieldName] = data[fieldName]
		}

		// Validation
		if fieldSchema.Required && (result[fieldName] == nil || result[fieldName] == "") {
			// Gérer l'erreur de validation
		}
	}

	// Ajouter les champs supplémentaires
	for key, value := range data {
		if _, exists := result[key]; !exists {
			result[key] = value
		}
	}

	return result
}

func (m *Model) getGoType(fieldType string) reflect.Type {
	switch strings.ToLower(fieldType) {
	case "string":
		return reflect.TypeOf("")
	case "number", "int":
		return reflect.TypeOf(int64(0))
	case "boolean", "bool":
		return reflect.TypeOf(true)
	case "date", "datetime":
		return reflect.TypeOf(time.Time{})
	case "array":
		return reflect.TypeOf("")
	default:
		return reflect.TypeOf(new(interface{})).Elem()
	}
}
