package crud

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	olclib "github.com/google/open-location-code/go"
	"gorm.io/gorm"
)

// ─────────────────────────────────────────────────────────────────────────────
// Update operators
// ─────────────────────────────────────────────────────────────────────────────

// applyUpdateOps applies MongoDB-style update operators to a target map.
// Rules:
//
//	prop: value          → same as prop: {$set: value}
//	prop: {$set: value}  → replace
//	prop: {$inc: n}      → increment (numeric)
//	prop: {$push: v}     → append to array
//	prop: {$pull: v}     → remove from array
//	prop: {$unset: true} → delete key
func applyUpdateOps(target map[string]any, patch map[string]any) {
	for key, val := range patch {
		opMap, isMap := val.(map[string]interface{})
		if !isMap {
			// plain value → implicit $set
			target[key] = val
			continue
		}
		// Check if any key starts with $
		hasOp := false
		for k := range opMap {
			if strings.HasPrefix(k, "$") {
				hasOp = true
				break
			}
		}
		if !hasOp {
			// plain nested map → $set the whole object
			target[key] = opMap
			continue
		}
		for op, opVal := range opMap {
			switch op {
			case "$set":
				target[key] = opVal
			case "$unset":
				delete(target, key)
			case "$inc":
				cur := toFloat(target[key])
				target[key] = cur + toFloat(opVal)
			case "$push":
				arr, _ := target[key].([]interface{})
				target[key] = append(arr, opVal)
			case "$pull":
				arr, _ := target[key].([]interface{})
				filtered := arr[:0]
				for _, item := range arr {
					if fmt.Sprint(item) != fmt.Sprint(opVal) {
						filtered = append(filtered, item)
					}
				}
				target[key] = filtered
			}
		}
	}
}

func toInt(v interface{}, def ...int64) int64 {
	d := int64(0)
	if len(def) > 0 {
		d = def[0]
	}
	switch n := v.(type) {
	case float32:
		return int64(n)
	case float64:
		return int64(n)
	case int:
		return int64(n)
	case int64:
		return n
	default:
		value := fmt.Sprintf("%v", v)
		if value == "" {
			return d
		}
		i, err := strconv.ParseInt(value, 10, 64)
		if err == nil {
			return i
		}
	}
	return d
}

func toFloat(v interface{}, def ...float64) float64 {
	d := 0.0
	if len(def) > 0 {
		d = def[0]
	}
	switch n := v.(type) {
	case float32:
		return float64(n)
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	default:
		value := fmt.Sprintf("%v", v)
		if value == "" {
			return d
		}
		i, err := strconv.ParseFloat(value, 64)
		if err == nil {
			return i
		}
	}
	return d
}

// ─────────────────────────────────────────────────────────────────────────────
// Collection — document-level operations
// ─────────────────────────────────────────────────────────────────────────────

// Collection is a scoped document accessor for one CrudSchema.
type Collection struct {
	db      *gorm.DB
	schema  *CrudSchema
	nsSlug  string
	hooks   *HookSet
	rc      *requestCtx
	baseDir string
}

// newCollection builds a Collection from a CrudSchema record.
func newCollection(db *gorm.DB, schema *CrudSchema, rc *requestCtx, baseDir string) (*Collection, error) {
	var hs HookSet
	if schema.Hooks != "" {
		if err := json.Unmarshal([]byte(schema.Hooks), &hs); err != nil {
			return nil, fmt.Errorf("collection: bad hooks JSON: %w", err)
		}
	}
	nsSlug := ""
	if rc != nil && rc.Namespace != nil {
		nsSlug = rc.Namespace.Slug
	} else {
		var ns Namespace
		db.Select("slug").First(&ns, "id = ?", schema.NamespaceID)
		nsSlug = ns.Slug
	}
	return &Collection{
		db:      db,
		schema:  schema,
		nsSlug:  nsSlug,
		hooks:   &hs,
		rc:      rc,
		baseDir: baseDir,
	}, nil
}

// rawToDoc converts a CrudDocument to a map[string]any for JS/API consumption.
func rawToDoc(d *CrudDocument) map[string]any {
	out := map[string]any{
		"id":           d.ID,
		"schema_id":    d.SchemaID,
		"namespace_id": d.NamespaceID,
		"created_at":   d.CreatedAt,
		"updated_at":   d.UpdatedAt,
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(d.Data), &data); err == nil {
		for k, v := range data {
			out[k] = v
		}
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(d.Meta), &meta); err == nil {
		out["_meta"] = meta
	}
	if d.Geo != "" {
		var geo map[string]any
		if err := json.Unmarshal([]byte(d.Geo), &geo); err == nil {
			out["_geo"] = geo
		}
	}
	return out
}

// hctx builds the hook context for the current collection operation.
func (col *Collection) hctx(doc, prev interface{}, docs interface{}) hookCtx {
	var u *User
	var ns *Namespace
	if col.rc != nil {
		u = col.rc.User
		ns = col.rc.Namespace
	}
	return hookCtx{
		user:      u,
		namespace: ns,
		schema:    col.schema,
		doc:       doc,
		docs:      docs,
		prev:      prev,
		baseDir:   col.baseDir,
	}
}

// ── List ──────────────────────────────────────────────────────────────────────

type ListOptions struct {
	Filter       map[string]any
	Sort         string
	Limit        int
	Offset       int
	SelectFields []string
}

func (col *Collection) List(opts ListOptions) ([]map[string]any, error) {
	q := col.db.Where("schema_id = ? AND namespace_id = ? AND deleted_at IS NULL",
		col.schema.ID, col.schema.NamespaceID)
	q = applyMongoFilter(q, opts.Filter, col.schema.AllowRawSQL, col.schema.Slug)
	if opts.Sort != "" {
		if strings.HasPrefix(opts.Sort, "-") {
			q = q.Order(opts.Sort[1:] + " DESC")
		} else {
			q = q.Order(opts.Sort + " ASC")
		}
	}
	if opts.Limit > 0 {
		q = q.Limit(opts.Limit)
	}
	if opts.Offset > 0 {
		q = q.Offset(opts.Offset)
	}

	var rows []CrudDocument
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}

	docs := make([]map[string]any, len(rows))
	for i, r := range rows {
		docs[i] = rawToDoc(&r)
	}

	// onList hook
	res, err := runHookSet(col.hooks, "onList", col.hctx(nil, nil, docs))
	if err != nil {
		return nil, err
	}
	if res.rejected {
		broadcastCRUD("reject", col.nsSlug, col.schema.Slug, "", res.message)
		return nil, rejectErr(res.message)
	}
	if res.modifiedDocs != nil {
		broadcastCRUD("list", col.nsSlug, col.schema.Slug, "", res.modifiedDocs)
		return res.modifiedDocs, nil
	}
	broadcastCRUD("list", col.nsSlug, col.schema.Slug, "", docs)
	return docs, nil
}

// ── FindOne ───────────────────────────────────────────────────────────────────

func (col *Collection) FindOne(id string) (map[string]any, error) {
	var row CrudDocument
	if err := col.db.Where(
		"id = ? AND schema_id = ? AND namespace_id = ? AND deleted_at IS NULL",
		id, col.schema.ID, col.schema.NamespaceID,
	).First(&row).Error; err != nil {
		return nil, fmt.Errorf("document not found: %w", err)
	}
	doc := rawToDoc(&row)

	res, err := runHookSet(col.hooks, "onRead", col.hctx(doc, nil, nil))
	if err != nil {
		return nil, err
	}
	if res.rejected {
		broadcastCRUD("reject", col.nsSlug, col.schema.Slug, id, res.message)
		return nil, rejectErr(res.message)
	}
	if res.modifiedDoc != nil {
		broadcastCRUD("read", col.nsSlug, col.schema.Slug, id, res.modifiedDoc)
		return res.modifiedDoc, nil
	}
	broadcastCRUD("read", col.nsSlug, col.schema.Slug, id, doc)
	return doc, nil
}

// ── Find (filter) ─────────────────────────────────────────────────────────────

func (col *Collection) Find(filter map[string]any, opts ListOptions) ([]map[string]any, error) {
	opts.Filter = filter
	return col.List(opts)
}

// ── Create ────────────────────────────────────────────────────────────────────

func (col *Collection) Create(data map[string]any, meta map[string]any) (map[string]any, error) {
	doc := rawDocFromMaps(data, meta)

	// onCreate hook
	res, err := runHookSet(col.hooks, "onCreate", col.hctx(doc, nil, nil))
	if err != nil {
		return nil, err
	}
	if res.rejected {
		broadcastCRUD("reject", col.nsSlug, col.schema.Slug, "", res.message)
		return nil, rejectErr(res.message)
	}
	if res.modifiedDoc != nil {
		doc = res.modifiedDoc
	}

	dataJSON, _ := json.Marshal(extractData(doc))
	metaJSON, _ := json.Marshal(extractMeta(doc))
	geoJSON, _ := json.Marshal(extractGeo(doc))

	row := CrudDocument{
		ID:          newID(),
		SchemaID:    col.schema.ID,
		NamespaceID: col.schema.NamespaceID,
		Data:        string(dataJSON),
		Meta:        string(metaJSON),
		Geo:         string(geoJSON),
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := col.db.Create(&row).Error; err != nil {
		return nil, err
	}
	out := rawToDoc(&row)
	broadcastCRUD("create", col.nsSlug, col.schema.Slug, row.ID, out)
	return out, nil
}

// ── Update ────────────────────────────────────────────────────────────────────

func (col *Collection) Update(id string, patch map[string]any, metaPatch map[string]any) (map[string]any, error) {
	var row CrudDocument
	if err := col.db.Where(
		"id = ? AND schema_id = ? AND namespace_id = ? AND deleted_at IS NULL",
		id, col.schema.ID, col.schema.NamespaceID,
	).First(&row).Error; err != nil {
		return nil, fmt.Errorf("document not found: %w", err)
	}

	prev := rawToDoc(&row)

	// Deserialize current data + meta
	var curData map[string]any
	var curMeta map[string]any
	json.Unmarshal([]byte(row.Data), &curData)
	json.Unmarshal([]byte(row.Meta), &curMeta)
	if curData == nil {
		curData = map[string]any{}
	}
	if curMeta == nil {
		curMeta = map[string]any{}
	}

	// Apply update operators
	applyUpdateOps(curData, patch)
	if metaPatch != nil {
		applyUpdateOps(curMeta, metaPatch)
	}

	// Merge into a single doc map for hooks
	docMap := rawToDoc(&row)
	for k, v := range curData {
		docMap[k] = v
	}
	docMap["_meta"] = curMeta

	// onUpdate hook
	res, err := runHookSet(col.hooks, "onUpdate", col.hctx(docMap, prev, nil))
	if err != nil {
		return nil, err
	}
	if res.rejected {
		broadcastCRUD("reject", col.nsSlug, col.schema.Slug, id, res.message)
		return nil, rejectErr(res.message)
	}
	if res.modifiedDoc != nil {
		docMap = res.modifiedDoc
		curData = extractData(docMap)
		curMeta = extractMeta(docMap)
	}

	dataJSON, _ := json.Marshal(curData)
	metaJSON, _ := json.Marshal(curMeta)
	geoStr := row.Geo
	if g := extractGeo(docMap); g != nil {
		if b, err := json.Marshal(g); err == nil {
			geoStr = string(b)
		}
	}

	if err := col.db.Model(&row).Updates(map[string]interface{}{
		"data":       string(dataJSON),
		"meta":       string(metaJSON),
		"geo":        geoStr,
		"updated_at": time.Now(),
	}).Error; err != nil {
		return nil, err
	}
	row.Data = string(dataJSON)
	row.Meta = string(metaJSON)
	row.Geo = geoStr
	row.UpdatedAt = time.Now()
	out := rawToDoc(&row)
	broadcastCRUD("update", col.nsSlug, col.schema.Slug, row.ID, out, prev)
	return out, nil
}

// ── Delete (soft) ─────────────────────────────────────────────────────────────

func (col *Collection) Delete(id string) error {
	var row CrudDocument
	if err := col.db.Where(
		"id = ? AND schema_id = ? AND namespace_id = ? AND deleted_at IS NULL",
		id, col.schema.ID, col.schema.NamespaceID,
	).First(&row).Error; err != nil {
		return fmt.Errorf("document not found: %w", err)
	}
	doc := rawToDoc(&row)

	res, err := runHookSet(col.hooks, "onDelete", col.hctx(doc, nil, nil))
	if err != nil {
		return err
	}
	if res.rejected {
		broadcastCRUD("reject", col.nsSlug, col.schema.Slug, id, res.message)
		return rejectErr(res.message)
	}

	now := time.Now()
	if col.schema.SoftDelete {
		err := col.db.Model(&row).Update("deleted_at", now).Error
		if err == nil {
			broadcastCRUD("delete", col.nsSlug, col.schema.Slug, id, nil)
		}
		return err
	}
	err = col.db.Delete(&row).Error
	if err == nil {
		broadcastCRUD("delete", col.nsSlug, col.schema.Slug, id, nil)
	}
	return err
}

// ── Trash ─────────────────────────────────────────────────────────────────────

func (col *Collection) TrashList(filter map[string]any) ([]map[string]any, error) {
	q := col.db.Where(
		"schema_id = ? AND namespace_id = ? AND deleted_at IS NOT NULL",
		col.schema.ID, col.schema.NamespaceID,
	)
	q = applyMongoFilter(q, filter, col.schema.AllowRawSQL, col.schema.Slug)

	var rows []CrudDocument
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}
	docs := make([]map[string]any, len(rows))
	for i, r := range rows {
		docs[i] = rawToDoc(&r)
	}

	res, err := runHookSet(col.hooks, "onListTrash", col.hctx(nil, nil, docs))
	if err != nil {
		return nil, err
	}
	if res.rejected {
		broadcastCRUD("reject", col.nsSlug, col.schema.Slug, "", res.message)
		return nil, rejectErr(res.message)
	}
	if res.modifiedDocs != nil {
		broadcastCRUD("listTrash", col.nsSlug, col.schema.Slug, "", res.modifiedDocs)
		return res.modifiedDocs, nil
	}
	broadcastCRUD("listTrash", col.nsSlug, col.schema.Slug, "", docs)
	return docs, nil
}

func (col *Collection) TrashFindOne(id string) (map[string]any, error) {
	var row CrudDocument
	if err := col.db.Where(
		"id = ? AND schema_id = ? AND namespace_id = ? AND deleted_at IS NOT NULL",
		id, col.schema.ID, col.schema.NamespaceID,
	).First(&row).Error; err != nil {
		return nil, fmt.Errorf("trashed document not found")
	}
	doc := rawToDoc(&row)

	res, err := runHookSet(col.hooks, "onReadTrash", col.hctx(doc, nil, nil))
	if err != nil {
		return nil, err
	}
	if res.rejected {
		broadcastCRUD("reject", col.nsSlug, col.schema.Slug, id, res.message)
		return nil, rejectErr(res.message)
	}
	if res.modifiedDoc != nil {
		broadcastCRUD("readTrash", col.nsSlug, col.schema.Slug, id, res.modifiedDoc)
		return res.modifiedDoc, nil
	}
	broadcastCRUD("readTrash", col.nsSlug, col.schema.Slug, id, doc)
	return doc, nil
}

func (col *Collection) TrashDelete(id string) error {
	var row CrudDocument
	if err := col.db.Where(
		"id = ? AND schema_id = ? AND namespace_id = ? AND deleted_at IS NOT NULL",
		id, col.schema.ID, col.schema.NamespaceID,
	).First(&row).Error; err != nil {
		return fmt.Errorf("trashed document not found")
	}
	doc := rawToDoc(&row)

	res, err := runHookSet(col.hooks, "onDeleteTrash", col.hctx(doc, nil, nil))
	if err != nil {
		return err
	}
	if res.rejected {
		broadcastCRUD("reject", col.nsSlug, col.schema.Slug, id, res.message)
		return rejectErr(res.message)
	}

	err = col.db.Unscoped().Delete(&row).Error
	if err == nil {
		broadcastCRUD("deleteTrash", col.nsSlug, col.schema.Slug, id, nil)
	}
	return err
}

func (col *Collection) Restore(id string) error {
	err := col.db.Model(&CrudDocument{}).
		Where("id = ? AND schema_id = ? AND namespace_id = ? AND deleted_at IS NOT NULL",
			id, col.schema.ID, col.schema.NamespaceID).
		Update("deleted_at", nil).Error
	if err == nil {
		broadcastCRUD("restore", col.nsSlug, col.schema.Slug, id, nil)
	}
	return err
}

// ── Geo queries ───────────────────────────────────────────────────────────────

type NearOptions struct {
	Lat, Lng    float64
	MaxDistance float64 // metres (approximated as degrees for SQLite)
}

func (col *Collection) Near(opts NearOptions) ([]map[string]any, error) {
	// Haversine approximation: 1 degree ≈ 111 km
	degRadius := opts.MaxDistance / 111000.0
	q := col.db.Where(
		`schema_id = ? AND namespace_id = ? AND deleted_at IS NULL
		AND (json_extract(geo,'$.coordinates[0]') - ?)*(json_extract(geo,'$.coordinates[0]') - ?)
		  + (json_extract(geo,'$.coordinates[1]') - ?)*(json_extract(geo,'$.coordinates[1]') - ?)
		  <= ? * ?`,
		col.schema.ID, col.schema.NamespaceID,
		opts.Lng, opts.Lng, opts.Lat, opts.Lat,
		degRadius, degRadius,
	).Order(fmt.Sprintf(
		`(json_extract(geo,'$.coordinates[0]') - %v)*(json_extract(geo,'$.coordinates[0]') - %v)
		+(json_extract(geo,'$.coordinates[1]') - %v)*(json_extract(geo,'$.coordinates[1]') - %v)`,
		opts.Lng, opts.Lng, opts.Lat, opts.Lat,
	))

	var rows []CrudDocument
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}
	docs := make([]map[string]any, len(rows))
	for i, r := range rows {
		docs[i] = rawToDoc(&r)
	}
	return docs, nil
}

type WithinOptions struct {
	// GeoJSON Polygon: {type:"Polygon",coordinates:[[[lng,lat],...]]}
	Polygon map[string]any
}

func (col *Collection) Within(opts WithinOptions) ([]map[string]any, error) {
	// Extract bounding box from the first ring of the polygon
	coords, ok := opts.Polygon["coordinates"].([]interface{})
	if !ok || len(coords) == 0 {
		return nil, fmt.Errorf("within: invalid polygon")
	}
	ring, ok := coords[0].([]interface{})
	if !ok {
		return nil, fmt.Errorf("within: invalid ring")
	}

	minLng, maxLng := 180.0, -180.0
	minLat, maxLat := 90.0, -90.0
	for _, pt := range ring {
		p, ok := pt.([]interface{})
		if !ok || len(p) < 2 {
			continue
		}
		lng := toFloat(p[0])
		lat := toFloat(p[1])
		if lng < minLng {
			minLng = lng
		}
		if lng > maxLng {
			maxLng = lng
		}
		if lat < minLat {
			minLat = lat
		}
		if lat > maxLat {
			maxLat = lat
		}
	}

	q := col.db.Where(
		`schema_id = ? AND namespace_id = ? AND deleted_at IS NULL
		AND json_extract(geo,'$.coordinates[0]') BETWEEN ? AND ?
		AND json_extract(geo,'$.coordinates[1]') BETWEEN ? AND ?`,
		col.schema.ID, col.schema.NamespaceID,
		minLng, maxLng, minLat, maxLat,
	)

	var rows []CrudDocument
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}
	docs := make([]map[string]any, len(rows))
	for i, r := range rows {
		docs[i] = rawToDoc(&r)
	}
	return docs, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// MongoDB-style filter on the JSON data column
// ─────────────────────────────────────────────────────────────────────────────

// applyMongoFilter translates a MongoDB-style filter map into GORM Where clauses.
// Fields are extracted from the JSON `data` column via json_extract().
//
// Special top-level operators:
//
//	$or / $and / $nor  — logical combinators (recursive)
//	$sql               — arbitrary SQL expression (string or {expr,args})
//
// Per-field operators (on data fields, geo column, arrays, and OLC strings):
//
//	$eq $ne $gt $gte $lt $lte $in $nin $exists $regex $mod
//	$near    — geo proximity filter  {$geometry|$center|$olc, $maxDistance}
//	$within  — geo containment       {$geometry|$box|$center+$radius}
//	$without — geo exclusion (NOT $within)
func applyMongoFilter(q *gorm.DB, filter map[string]any, allowRawSQL bool, schemaSlug string) *gorm.DB {
	if filter == nil {
		return q
	}
	for key, val := range filter {
		// ── Top-level special operators ───────────────────────────────────────
		switch key {
		case "$or", "$and", "$nor":
			subs, ok := val.([]interface{})
			if !ok {
				continue
			}
			var inner *gorm.DB
			for _, sf := range subs {
				m, ok := sf.(map[string]interface{})
				if !ok {
					continue
				}
				sub := applyMongoFilter(q.Session(&gorm.Session{NewDB: true}), m, allowRawSQL, schemaSlug)
				if inner == nil {
					inner = sub
				} else if key == "$or" || key == "$nor" {
					inner = inner.Or(sub)
				} else {
					inner = inner.Where(sub)
				}
			}
			if inner != nil {
				if key == "$nor" {
					q = q.Not(inner)
				} else {
					q = q.Where(inner)
				}
			}
			continue

		case "$sql":
			if !allowRawSQL {
				fmt.Printf("CRUD: $sql operator blocked for schema %q (AllowRawSQL=false)\n", schemaSlug)
				continue
			}
			// $sql: "price * 1.2 > 100"
			// $sql: { expr: "json_extract(data,'$.price') > ?", args: [100] }
			switch v := val.(type) {
			case string:
				if v != "" {
					q = q.Where(v)
				}
			case map[string]interface{}:
				expr, _ := v["expr"].(string)
				if expr == "" {
					continue
				}
				var args []interface{}
				if a, ok := v["args"].([]interface{}); ok {
					args = a
				}
				q = q.Where(expr, args...)
			}
			continue
		}

		// ── Per-field operators ───────────────────────────────────────────────
		// col is the SQL expression to extract the field value.
		// For the special "geo" column name we also expose the raw geo column.
		dataCol := fmt.Sprintf("json_extract(data, '$.%s')", key)

		opMap, isMap := val.(map[string]interface{})
		if !isMap {
			q = q.Where(dataCol+" = ?", val)
			continue
		}

		for op, opVal := range opMap {
			switch op {
			// ── Comparison ─────────────────────────────────────────────────────
			case "$eq":
				q = q.Where(dataCol+" = ?", opVal)
			case "$ne":
				q = q.Where(dataCol+" != ?", opVal)
			case "$gt":
				q = q.Where(dataCol+" > ?", opVal)
			case "$gte":
				q = q.Where(dataCol+" >= ?", opVal)
			case "$lt":
				q = q.Where(dataCol+" < ?", opVal)
			case "$lte":
				q = q.Where(dataCol+" <= ?", opVal)
			case "$in":
				q = q.Where(dataCol+" IN ?", opVal)
			case "$nin":
				q = q.Where(dataCol+" NOT IN ?", opVal)
			case "$exists":
				if b, ok := opVal.(bool); ok {
					if b {
						q = q.Where(dataCol + " IS NOT NULL")
					} else {
						q = q.Where(dataCol + " IS NULL")
					}
				}
			case "$regex":
				q = q.Where(dataCol+" REGEXP ?", opVal)
			case "$mod":
				if arr, ok := opVal.([]interface{}); ok && len(arr) == 2 {
					q = q.Where(dataCol+` % ? = ?`, arr[0], arr[1])
				}

			// ── Geo: $near ─────────────────────────────────────────────────────
			//
			// Supports three coordinate sources on any field:
			//   $geometry : GeoJSON Point  {type:"Point",coordinates:[lng,lat]}
			//   $center   : [lng, lat] array
			//   $olc      : Open Location Code string (Plus Code)
			//
			// The field may contain:
			//   - a GeoJSON object  stored in the dedicated `geo` column
			//   - a [lng,lat] array stored as JSON in the `data` column
			//   - an OLC string     stored as text in the `data` column
			//
			// $maxDistance is in metres (approximated as degrees: 1° ≈ 111 km).
			case "$near":
				m, ok := opVal.(map[string]interface{})
				if !ok {
					continue
				}
				lng, lat, ok := extractGeoCenter(m)
				if !ok {
					continue
				}
				maxDist := toFloat64(m["$maxDistance"]) // metres
				degR := maxDist / 111000.0
				// Build SQL expression that works for all three field types
				lngExpr, latExpr := geoFieldExpr(key)
				distExpr := fmt.Sprintf(
					"((%s) - ?)*((%s) - ?) + ((%s) - ?)*((%s) - ?) <= ? * ?",
					lngExpr, lngExpr, latExpr, latExpr,
				)
				orderExpr := fmt.Sprintf(
					"((%s) - %v)*((%s) - %v) + ((%s) - %v)*((%s) - %v)",
					lngExpr, lng, lngExpr, lng, latExpr, lat, latExpr, lat,
				)
				q = q.Where(distExpr, lng, lng, lat, lat, degR, degR).Order(orderExpr)

			// ── Geo: $within ───────────────────────────────────────────────────
			//
			// Supports:
			//   $geometry : GeoJSON Polygon  → bounding-box approximation
			//   $box      : [[minLng,minLat],[maxLng,maxLat]]
			//   $center + $radius : circle (radius in degrees)
			case "$within":
				m, ok := opVal.(map[string]interface{})
				if !ok {
					continue
				}
				lngExpr, latExpr := geoFieldExpr(key)
				if expr, args := buildWithinExpr(m, lngExpr, latExpr); expr != "" {
					q = q.Where(expr, args...)
				}

			// ── Geo: $without — NOT within ─────────────────────────────────────
			case "$without":
				m, ok := opVal.(map[string]interface{})
				if !ok {
					continue
				}
				lngExpr, latExpr := geoFieldExpr(key)
				if expr, args := buildWithinExpr(m, lngExpr, latExpr); expr != "" {
					q = q.Not(expr, args...)
				}
			}
		}
	}
	return q
}

// ─────────────────────────────────────────────────────────────────────────────
// Geo helpers
// ─────────────────────────────────────────────────────────────────────────────

// geoFieldExpr returns the SQL expressions to extract (longitude, latitude)
// from a field that may be:
//
//  1. The special field name "geo" — uses the dedicated `geo` column (GeoJSON).
//  2. Any other field name — tries json_extract from the `data` column.
//     Works for both [lng, lat] arrays and GeoJSON objects stored in data.
//
// For OLC strings the calling site must decode first (see extractGeoCenter).
func geoFieldExpr(fieldName string) (lngExpr, latExpr string) {
	if fieldName == "geo" || fieldName == "_geo" {
		// Dedicated geo column stores a GeoJSON Point
		return "json_extract(geo,'$.coordinates[0]')",
			"json_extract(geo,'$.coordinates[1]')"
	}
	// data field may be a [lng,lat] array or a GeoJSON Point object
	// Try GeoJSON coordinates first, fall back to array index
	// SQLite coalesces: if $.field.coordinates[0] is null, use $.field[0]
	return fmt.Sprintf("COALESCE(json_extract(data,'$.%s.coordinates[0]'), json_extract(data,'$.%s[0]'))", fieldName, fieldName),
		fmt.Sprintf("COALESCE(json_extract(data,'$.%s.coordinates[1]'), json_extract(data,'$.%s[1]'))", fieldName, fieldName)
}

// extractGeoCenter reads (lng, lat) from a geo operator value map.
// Supports three formats:
//
//	$geometry : {"type":"Point","coordinates":[lng,lat]}
//	$center   : [lng, lat]
//	$olc      : "8FW4V75V+8Q"  (Open Location Code / Plus Code)
func extractGeoCenter(m map[string]interface{}) (lng, lat float64, ok bool) {
	// $geometry — GeoJSON Point
	if geom, exists := m["$geometry"]; exists {
		gm, isMap := geom.(map[string]interface{})
		if !isMap {
			return
		}
		coords, _ := gm["coordinates"].([]interface{})
		if len(coords) >= 2 {
			lng = toFloat64(coords[0])
			lat = toFloat64(coords[1])
			ok = true
		}
		return
	}

	// $center — [lng, lat] array
	if center, exists := m["$center"]; exists {
		arr, isArr := center.([]interface{})
		if isArr && len(arr) >= 2 {
			lng = toFloat64(arr[0])
			lat = toFloat64(arr[1])
			ok = true
		}
		return
	}

	// $olc — Open Location Code (Plus Code)
	if olcVal, exists := m["$olc"]; exists {
		if code, isStr := olcVal.(string); isStr {
			if decoded, err := decodeOLC(code); err == nil {
				lng = decoded[0]
				lat = decoded[1]
				ok = true
			}
		}
	}
	return
}

// buildWithinExpr builds a SQL WHERE expression for $within / $without.
// Returns ("", nil) when the spec cannot be parsed.
func buildWithinExpr(m map[string]interface{}, lngExpr, latExpr string) (string, []interface{}) {
	// ── $box: [[minLng,minLat],[maxLng,maxLat]] ──────────────────────────────
	if box, ok := m["$box"].([]interface{}); ok && len(box) == 2 {
		p1, ok1 := box[0].([]interface{})
		p2, ok2 := box[1].([]interface{})
		if ok1 && ok2 && len(p1) >= 2 && len(p2) >= 2 {
			minLng, minLat := toFloat64(p1[0]), toFloat64(p1[1])
			maxLng, maxLat := toFloat64(p2[0]), toFloat64(p2[1])
			expr := fmt.Sprintf("(%s) BETWEEN ? AND ? AND (%s) BETWEEN ? AND ?",
				lngExpr, latExpr)
			return expr, []interface{}{minLng, maxLng, minLat, maxLat}
		}
	}

	// ── $center + $radius (circle, radius in degrees) ───────────────────────
	if center, ok := m["$center"].([]interface{}); ok && len(center) >= 2 {
		lng := toFloat64(center[0])
		lat := toFloat64(center[1])
		radius := toFloat64(m["$radius"])
		expr := fmt.Sprintf(
			"((%s) - ?)*((%s) - ?) + ((%s) - ?)*((%s) - ?) <= ? * ?",
			lngExpr, lngExpr, latExpr, latExpr,
		)
		return expr, []interface{}{lng, lng, lat, lat, radius, radius}
	}

	// ── $geometry — GeoJSON Polygon → bounding-box approximation ────────────
	if geom, ok := m["$geometry"].(map[string]interface{}); ok {
		coords, _ := geom["coordinates"].([]interface{})
		if len(coords) == 0 {
			return "", nil
		}
		ring, _ := coords[0].([]interface{})
		if len(ring) == 0 {
			return "", nil
		}
		minLng, maxLng := math.Inf(1), math.Inf(-1)
		minLat, maxLat := math.Inf(1), math.Inf(-1)
		for _, pt := range ring {
			p, ok := pt.([]interface{})
			if !ok || len(p) < 2 {
				continue
			}
			ln := toFloat64(p[0])
			lt := toFloat64(p[1])
			if ln < minLng {
				minLng = ln
			}
			if ln > maxLng {
				maxLng = ln
			}
			if lt < minLat {
				minLat = lt
			}
			if lt > maxLat {
				maxLat = lt
			}
		}
		if math.IsInf(minLng, 1) {
			return "", nil
		}
		expr := fmt.Sprintf("(%s) BETWEEN ? AND ? AND (%s) BETWEEN ? AND ?",
			lngExpr, latExpr)
		return expr, []interface{}{minLng, maxLng, minLat, maxLat}
	}

	return "", nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Open Location Code (OLC / Plus Codes) decoder
// ─────────────────────────────────────────────────────────────────────────────
//
// Implements a minimal decode of full OLC codes (10+ digits) without external
// dependencies.  Enough precision for proximity filtering at the sub-metre level.
// Spec: https://github.com/google/open-location-code/blob/main/docs/reference/index.md

const (
	olcAlphabet  = "23456789CFGHJMPQRVWX"
	olcSepChar   = '+'
	olcMinLength = 10 // minimum digits for a "full" code (globally unambiguous)
)

// decodeOLC decodes an Open Location Code and returns [longitude, latitude]
// of the centre of the code area, or an error.
// refLoc est optionnel : refLoc[0] = lon, refLoc[1] = lat.
//
// pt, err := decodeOLC("V8F2+GX", 48.8566, 2.3522)
//
// pt, err := decodeOLC("8FW4V8F2+GX")
func decodeOLC(code string, refLoc ...float64) ([2]float64, error) {
	var fullCode string
	var err error

	// Dans la lib Go de Google, on utilise CheckFull pour valider l'état "complet"
	if errFull := olclib.CheckFull(code); errFull == nil {
		fullCode = code
	} else {
		// Si ce n'est pas un code complet, on vérifie si c'est un code court valide
		if errShort := olclib.CheckShort(code); errShort != nil {
			return [2]float64{}, fmt.Errorf("code invalide (ni complet, ni court): %v", errShort)
		}

		// Pour un code court, les coordonnées de référence sont obligatoires
		if len(refLoc) < 2 {
			return [2]float64{}, fmt.Errorf("le code court '%s' nécessite lat/lon de référence", code)
		}

		// On complète le code court avec la position de référence
		fullCode, err = olclib.RecoverNearest(code, refLoc[1], refLoc[0])
		if err != nil {
			return [2]float64{}, fmt.Errorf("erreur RecoverNearest: %v", err)
		}
	}

	// Décodage final
	area, err := olclib.Decode(fullCode)
	if err != nil {
		return [2]float64{}, fmt.Errorf("erreur de décodage: %v", err)
	}

	lat, lng := area.Center()
	return [2]float64{lng, lat}, nil
}

// toFloat64 safely converts an interface{} numeric to float64.
func toFloat64(v interface{}) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case int32:
		return float64(n)
	case uint:
		return float64(n)
	case uint64:
		return float64(n)
	case string:
		if f, err := strconv.ParseFloat(n, 64); err == nil {
			return f
		}
	}
	return 0
}

// ─────────────────────────────────────────────────────────────────────────────
// Map helpers
// ─────────────────────────────────────────────────────────────────────────────

func rawDocFromMaps(data, meta map[string]any) map[string]any {
	out := make(map[string]any, len(data)+1)
	for k, v := range data {
		out[k] = v
	}
	if meta != nil {
		out["_meta"] = meta
	}
	return out
}

func extractData(doc map[string]any) map[string]any {
	out := make(map[string]any)
	for k, v := range doc {
		if k != "_meta" && k != "_geo" &&
			k != "id" && k != "schema_id" && k != "namespace_id" &&
			k != "created_at" && k != "updated_at" {
			out[k] = v
		}
	}
	return out
}

func extractMeta(doc map[string]any) map[string]any {
	if m, ok := doc["_meta"].(map[string]interface{}); ok {
		return m
	}
	return map[string]any{}
}

func extractGeo(doc map[string]any) map[string]any {
	if g, ok := doc["_geo"].(map[string]interface{}); ok {
		return g
	}
	return nil
}
