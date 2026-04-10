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
	model        *Model
	db           *gorm.DB
	err          error
	vm           *goja.Runtime
	selectFields []string
	omitFields   []string
}

func NewQuery(model *Model, vm *goja.Runtime) *Query {
	return &Query{
		model: model,
		db:    model.db.Table(model.Name),
		vm:    vm,
	}
}

func (q *Query) Filter(filter map[string]interface{}) *Query {
	q.db = q.processFilter(q.db, filter)
	return q
}

func (q *Query) processFilter(db *gorm.DB, filter map[string]interface{}) *gorm.DB {
	for key, value := range filter {
		switch key {
		case "$or", "$and", "$nor":
			if subFilters, ok := value.([]interface{}); ok {
				var innerDB *gorm.DB
				for _, sf := range subFilters {
					if m, ok := sf.(map[string]interface{}); ok {
						subCond := q.processFilter(q.model.db.Session(&gorm.Session{}), m)
						if innerDB == nil {
							innerDB = subCond
						} else {
							if key == "$or" || key == "$nor" {
								innerDB = innerDB.Or(subCond)
							} else {
								innerDB = innerDB.Where(subCond)
							}
						}
					}
				}
				if innerDB != nil {
					if key == "$nor" {
						db = db.Not(innerDB)
					} else {
						db = db.Where(innerDB)
					}
				}
			}
		case "$not":
			if m, ok := value.(map[string]interface{}); ok {
				db = db.Not(q.processFilter(q.model.db.Session(&gorm.Session{}), m))
			}
		case "$comment":
			// No-op or log
		case "$where":
			if sql, ok := value.(string); ok {
				db = db.Where(sql)
			}
		default:
			if subMap, ok := value.(map[string]interface{}); ok {
				for op, val := range subMap {
					switch op {
					case "$eq":
						db = db.Where(fmt.Sprintf("%s = ?", key), val)
					case "$gt":
						db = db.Where(fmt.Sprintf("%s > ?", key), val)
					case "$gte":
						db = db.Where(fmt.Sprintf("%s >= ?", key), val)
					case "$lt":
						db = db.Where(fmt.Sprintf("%s < ?", key), val)
					case "$lte":
						db = db.Where(fmt.Sprintf("%s <= ?", key), val)
					case "$ne":
						db = db.Where(fmt.Sprintf("%s != ?", key), val)
					case "$in":
						db = db.Where(fmt.Sprintf("%s IN ?", key), val)
					case "$nin":
						db = db.Where(fmt.Sprintf("%s NOT IN ?", key), val)
					case "$regex":
						db = db.Where(fmt.Sprintf("%s REGEXP ?", key), val)
					case "$exists":
						if b, ok := val.(bool); ok {
							if b {
								db = db.Where(fmt.Sprintf("%s IS NOT NULL", key))
							} else {
								db = db.Where(fmt.Sprintf("%s IS NULL", key))
							}
						}
					case "$mod":
						if arr, ok := val.([]interface{}); ok && len(arr) == 2 {
							db = db.Where(fmt.Sprintf("%s %% ? = ?", key), arr[0], arr[1])
						}
					case "$all":
						// Simplified: must contain all (comma separated list assumed for SQL strings)
						if arr, ok := val.([]interface{}); ok {
							for _, item := range arr {
								db = db.Where(fmt.Sprintf("%s LIKE ?", key), "%"+fmt.Sprint(item)+"%")
							}
						}
					case "$size":
						// SQLite specific hack for length of comma-sep list
						db = db.Where(fmt.Sprintf("(LENGTH(%s) - LENGTH(REPLACE(%s, ',', '')) + 1) = ?", key, key), val)
					case "$type":
						// SQLite typeof()
						db = db.Where(fmt.Sprintf("typeof(%s) = ?", key), val)
					case "$geoWithin":
						if m, ok := val.(map[string]interface{}); ok {
							if box, ok := m["$box"].([]interface{}); ok && len(box) == 2 {
								p1 := box[0].([]interface{})
								p2 := box[1].([]interface{})
								db = db.Where(fmt.Sprintf("json_extract(%[1]s, '$[0]') BETWEEN ? AND ? AND json_extract(%[1]s, '$[1]') BETWEEN ? AND ?", key), p1[0], p2[0], p1[1], p2[1])
							} else if center, ok := m["$centerSphere"].([]interface{}); ok && len(center) == 2 {
								p := center[0].([]interface{})
								radius := center[1]
								db = db.Where(fmt.Sprintf("(json_extract(%[1]s, '$[0]') - ?)*(json_extract(%[1]s, '$[0]') - ?) + (json_extract(%[1]s, '$[1]') - ?)*(json_extract(%[1]s, '$[1]') - ?) <= ? * ?", key), p[0], p[0], p[1], p[1], radius, radius)
							}
						}
					case "$nearSphere":
						if m, ok := val.(map[string]interface{}); ok {
							var cx, cy interface{}
							if geom, ok := m["$geometry"].(map[string]interface{}); ok {
								if coords, ok := geom["coordinates"].([]interface{}); ok && len(coords) == 2 {
									cx, cy = coords[0], coords[1]
								}
							}
							if cx != nil && cy != nil {
								distExpr := fmt.Sprintf("(json_extract(%[1]s, '$[0]') - ?)*(json_extract(%[1]s, '$[0]') - ?) + (json_extract(%[1]s, '$[1]') - ?)*(json_extract(%[1]s, '$[1]') - ?)", key)
								if maxDist, ok := m["$maxDistance"]; ok {
									db = db.Where(distExpr+" <= ? * ?", cx, cx, cy, cy, maxDist, maxDist)
								}
								db = db.Order(distExpr) // Sort by proximity
							}
						}
					case "$geoIntersects":
						// For points in SQLite, intersects is equivalent to equality or within a very small tolerance
						if m, ok := val.(map[string]interface{}); ok {
							if geom, ok := m["$geometry"].(map[string]interface{}); ok {
								if coords, ok := geom["coordinates"].([]interface{}); ok && len(coords) == 2 {
									db = db.Where(fmt.Sprintf("json_extract(%[1]s, '$[0]') = ? AND json_extract(%[1]s, '$[1]') = ?", key), coords[0], coords[1])
								}
							}
						}
					}
				}
			} else {
				// Egalité simple
				db = db.Where(fmt.Sprintf("%s = ?", key), value)
			}
		}
	}
	return db
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
			fieldName := p[1:]
			q.db = q.db.Omit(fieldName)
			q.omitFields = append(q.omitFields, fieldName)
		} else {
			q.db = q.db.Select(p)
			q.selectFields = append(q.selectFields, p)
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
		data := structToMap(elem, q.selectFields, q.omitFields)
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

	data := structToMap(reflect.ValueOf(result).Elem(), q.selectFields, q.omitFields)
	return &Document{
		Data:  data,
		Model: q.model,
		ID:    fmt.Sprintf("%v", data["id"]),
		isNew: false,
	}, nil
}

func (q *Query) ToJSObject(returnFirstOnly ...bool) goja.Value {
	obj := q.vm.NewObject()

	obj.Set("sort", func(s string) goja.Value {
		q.Sort(s)
		return obj
	})
	obj.Set("limit", func(n int) goja.Value {
		if len(returnFirstOnly) > 0 && returnFirstOnly[0] {
			n = 1
		}
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
		
		if len(returnFirstOnly) > 0 && returnFirstOnly[0] {
			if len(docs) == 0 {
				return goja.Undefined()
			}
			return docs[0].ToJSObject(q.vm)
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

		if len(returnFirstOnly) > 0 && returnFirstOnly[0] {
			var res goja.Value = goja.Undefined()
			if len(docs) > 0 {
				res = docs[0].ToJSObject(q.vm)
			}
			if !goja.IsUndefined(onFulfilled) {
				if fn, ok := goja.AssertFunction(onFulfilled); ok {
					fn(goja.Undefined(), res)
				}
			}
			return res
		} else {
			var jsDocs []goja.Value
			for _, d := range docs {
				jsDocs = append(jsDocs, d.ToJSObject(q.vm))
			}
			res := q.vm.ToValue(jsDocs)
			if !goja.IsUndefined(onFulfilled) {
				if fn, ok := goja.AssertFunction(onFulfilled); ok {
					fn(goja.Undefined(), res)
				}
			}
			return res
		}
	})

	return obj
}

func structToMap(val reflect.Value, selectFields []string, omitFields []string) map[string]interface{} {
	data := make(map[string]interface{})
	typ := val.Type()
	for i := 0; i < val.NumField(); i++ {
		field := val.Field(i)
		fieldType := typ.Field(i)
		jsonTag := string(fieldType.Tag.Get("json"))
		if jsonTag != "" {
			fieldName := strings.Split(jsonTag, ",")[0]
			if fieldName != "" && fieldName != "-" {
				// Filter based on selects/omits
				allowed := true
				if len(selectFields) > 0 {
					allowed = false
					for _, s := range selectFields {
						if s == fieldName || s == "id" {
							allowed = true
							break
						}
					}
				}
				if allowed && len(omitFields) > 0 {
					for _, o := range omitFields {
						if o == fieldName && o != "id" {
							allowed = false
							break
						}
					}
				}

				if allowed {
					val := field.Interface()
					data[fieldName] = val
				}
			}
		}
	}
	return data
}
