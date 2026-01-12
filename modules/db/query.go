package db

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/dop251/goja"
	"gorm.io/gorm"
)

// Query - Gestionnaire de requêtes chaînables
type Query struct {
	model *Model
	db    *gorm.DB
	err   error
	vm    *goja.Runtime
}

func NewQuery(model *Model, vm *goja.Runtime) *Query {
	return &Query{
		model: model,
		db:    model.db.Table(model.Name),
		vm:    vm,
	}
}

func (q *Query) Filter(filter map[string]interface{}) *Query {
	for key, value := range filter {
		if strings.HasPrefix(key, "$") {
			// Opérateurs globaux (ex: $or, $and)
			// TODO: Implémenter or/and
			continue
		}

		if subMap, ok := value.(map[string]interface{}); ok {
			// Opérateurs de champ (ex: { age: { $gt: 18 } })
			for op, val := range subMap {
				switch op {
				case "$gt":
					q.db = q.db.Where(fmt.Sprintf("%s > ?", key), val)
				case "$gte":
					q.db = q.db.Where(fmt.Sprintf("%s >= ?", key), val)
				case "$lt":
					q.db = q.db.Where(fmt.Sprintf("%s < ?", key), val)
				case "$lte":
					q.db = q.db.Where(fmt.Sprintf("%s <= ?", key), val)
				case "$ne":
					q.db = q.db.Where(fmt.Sprintf("%s != ?", key), val)
				case "$in":
					q.db = q.db.Where(fmt.Sprintf("%s IN ?", key), val)
				case "$nin":
					q.db = q.db.Where(fmt.Sprintf("%s NOT IN ?", key), val)
				case "$regex":
					// Simplifié pour SQLite/Postgres
					q.db = q.db.Where(fmt.Sprintf("%s REGEXP ?", key), val)
				}
			}
		} else {
			// Egalité simple
			q.db = q.db.Where(fmt.Sprintf("%s = ?", key), value)
		}
	}
	return q
}

func (q *Query) Sort(s string) *Query {
	// Mongoose supporte "field" ou "-field"
	if strings.HasPrefix(s, "-") {
		q.db = q.db.Order(fmt.Sprintf("%s DESC", s[1:]))
	} else {
		q.db = q.db.Order(fmt.Sprintf("%s ASC", s))
	}
	return q
}

func (q *Query) Limit(n int) *Query {
	q.db = q.db.Limit(n)
	return q
}

func (q *Query) Skip(n int) *Query {
	q.db = q.db.Offset(n)
	return q
}

func (q *Query) Select(fields string) *Query {
	// "name age -password"
	parts := strings.Split(fields, " ")
	for _, p := range parts {
		if strings.HasPrefix(p, "-") {
			q.db = q.db.Omit(p[1:])
		} else {
			q.db = q.db.Select(p)
		}
	}
	return q
}

func (q *Query) Exec() ([]*Document, error) {
	structType := q.model.createStructType()
	sliceType := reflect.SliceOf(structType)
	slicePtr := reflect.New(sliceType)

	if err := q.db.Find(slicePtr.Interface()).Error; err != nil {
		return nil, err
	}

	sliceValue := slicePtr.Elem()
	var docs []*Document
	for i := 0; i < sliceValue.Len(); i++ {
		elem := sliceValue.Index(i)
		data := structToMap(elem)
		docs = append(docs, &Document{
			Data:  data,
			Model: q.model,
			ID:    fmt.Sprintf("%v", data["id"]),
			isNew: false,
		})
	}
	return docs, nil
}

func (q *Query) ExecOne() (*Document, error) {
	structType := q.model.createStructType()
	result := reflect.New(structType).Interface()

	if err := q.db.First(result).Error; err != nil {
		return nil, err
	}

	data := structToMap(reflect.ValueOf(result).Elem())
	return &Document{
		Data:  data,
		Model: q.model,
		ID:    fmt.Sprintf("%v", data["id"]),
		isNew: false,
	}, nil
}

func (q *Query) ToJSObject() goja.Value {
	obj := q.vm.NewObject()

	obj.Set("sort", func(s string) goja.Value {
		q.Sort(s)
		return obj
	})
	obj.Set("limit", func(n int) goja.Value {
		q.Limit(n)
		return obj
	})
	obj.Set("skip", func(n int) goja.Value {
		q.Skip(n)
		return obj
	})
	obj.Set("select", func(s string) goja.Value {
		q.Select(s)
		return obj
	})

	obj.Set("exec", func() goja.Value {
		docs, err := q.Exec()
		if err != nil {
			panic(q.vm.ToValue(err))
		}
		var jsDocs []goja.Value
		for _, d := range docs {
			jsDocs = append(jsDocs, d.ToJSObject(q.vm))
		}
		return q.vm.ToValue(jsDocs)
	})

	// Support thenable for await query
	obj.Set("then", func(onFulfilled, onRejected goja.Value) goja.Value {
		docs, err := q.Exec()
		if err != nil {
			if !goja.IsUndefined(onRejected) {
				if fn, ok := goja.AssertFunction(onRejected); ok {
					fn(goja.Undefined(), q.vm.ToValue(err))
				}
			}
			return goja.Undefined()
		}
		var jsDocs []goja.Value
		for _, d := range docs {
			jsDocs = append(jsDocs, d.ToJSObject(q.vm))
		}
		if fn, ok := goja.AssertFunction(onFulfilled); ok {
			fn(goja.Undefined(), q.vm.ToValue(jsDocs))
		}
		return q.vm.ToValue(jsDocs)
	})

	return obj
}

func structToMap(val reflect.Value) map[string]interface{} {
	data := make(map[string]interface{})
	typ := val.Type()
	for i := 0; i < val.NumField(); i++ {
		field := val.Field(i)
		fieldType := typ.Field(i)
		jsonTag := string(fieldType.Tag.Get("json"))
		if jsonTag != "" {
			fieldName := strings.Split(jsonTag, ",")[0]
			if fieldName != "" && fieldName != "-" {
				val := field.Interface()
				data[fieldName] = val
			}
		}
	}
	return data
}
