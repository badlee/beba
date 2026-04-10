package crud

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	_ "modernc.org/sqlite"
)

func setupTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.New(sqlite.Config{
		DriverName: "sqlite",
		DSN:        "file::memory:?cache=shared",
	}), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open in-memory db: %v", err)
	}

	if err := Migrate(db); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	if err := Seed(db); err != nil {
		t.Fatalf("failed to seed: %v", err)
	}

	return db
}

func TestMigrateAndSeed(t *testing.T) {
	db := setupTestDB(t)

	var ns Namespace
	if err := db.Where("slug = ?", "global").First(&ns).Error; err != nil {
		t.Fatalf("global namespace not found: %v", err)
	}

	var adminRole Role
	if err := db.Where("name = ? AND namespace_id = ?", "admin", ns.ID).First(&adminRole).Error; err != nil {
		t.Fatalf("admin role not found: %v", err)
	}

	var viewerRole Role
	if err := db.Where("name = ? AND namespace_id = ?", "viewer", ns.ID).First(&viewerRole).Error; err != nil {
		t.Fatalf("viewer role not found: %v", err)
	}
}

func TestCollectionOperations(t *testing.T) {
	db := setupTestDB(t)

	var ns Namespace
	db.Where("slug = ?", "global").First(&ns)

	// Create a schema
	schema := CrudSchema{
		ID:          newID(),
		Name:        "Test Item",
		Slug:        "test_items",
		NamespaceID: ns.ID,
		SoftDelete:  true,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	db.Create(&schema)

	// Build collection
	col, err := newCollection(db, &schema, nil, "")
	if err != nil {
		t.Fatalf("failed to create collection: %v", err)
	}

	// Test Create
	doc, err := col.Create(map[string]any{"title": "Item 1", "count": 10}, map[string]any{"author": "test"})
	if err != nil {
		t.Fatalf("failed to create document: %v", err)
	}
	if doc["title"] != "Item 1" {
		t.Errorf("expected title 'Item 1', got %v", doc["title"])
	}
	id, ok := doc["id"].(string)
	if !ok || id == "" {
		t.Fatalf("invalid generated id")
	}

	// Test Update using MongoDB Operators ($inc)
	updated, err := col.Update(id, map[string]any{"count": map[string]any{"$inc": 5}}, nil)
	if err != nil {
		t.Fatalf("failed to update document: %v", err)
	}
	// Note: the count value returned by updated dictionary might be float64 or int format
	if updated["count"] != float64(15) && updated["count"] != int64(15) && updated["count"] != 15 {
		t.Errorf("expected count 15, got %v (%T)", updated["count"], updated["count"])
	}

	// Test FindOne
	found, err := col.FindOne(id)
	if err != nil {
		t.Fatalf("failed to find document: %v", err)
	}
	if found["title"] != "Item 1" {
		t.Errorf("expected title 'Item 1', got %v", found["title"])
	}

	// Test List / Find with advanced filtering ($gt)
	items, err := col.Find(map[string]any{"count": map[string]any{"$gt": 10}}, ListOptions{})
	if err != nil {
		t.Fatalf("failed to find documents: %v", err)
	}
	if len(items) != 1 {
		t.Errorf("expected 1 item, got %d", len(items))
	}

	// Test Soft Delete
	if err := col.Delete(id); err != nil {
		t.Fatalf("failed to delete document: %v", err)
	}
	_, err = col.FindOne(id)
	if err == nil {
		t.Errorf("expected error when finding deleted document")
	}

	// Test TrashList
	trashed, err := col.TrashList(nil)
	if err != nil {
		t.Fatalf("failed to list trashed documents: %v", err)
	}
	if len(trashed) != 1 {
		t.Errorf("expected 1 trashed item, got %d", len(trashed))
	}

	// Test Restore
	if err := col.Restore(id); err != nil {
		t.Fatalf("failed to restore document: %v", err)
	}
	_, err = col.FindOne(id)
	if err != nil {
		t.Errorf("expected to find document after restore, got error: %v", err)
	}

	// Test Hard Delete from Trash
	col.Delete(id) // soft delete again
	if err := col.TrashDelete(id); err != nil {
		t.Fatalf("failed to hard delete document: %v", err)
	}
	trashedAfter, _ := col.TrashList(nil)
	if len(trashedAfter) != 0 {
		t.Errorf("expected 0 trashed items after hard delete, got %d", len(trashedAfter))
	}
}

func TestAuthLogic(t *testing.T) {
	db := setupTestDB(t)

	var ns Namespace
	db.Where("slug = ?", "global").First(&ns)

	// In module.go, bcryptHash is instantiated in init(), ensure it works
	if bcryptHash == nil {
		t.Skip("bcryptHash is not initialized in tests context")
	}

	// Create a user
	user := User{
		ID:           newID(),
		Username:     "testuser",
		Email:        "test@example.com",
		PasswordHash: bcryptHash("password123"),
		NamespaceID:  &ns.ID,
		IsActive:     true,
	}
	db.Create(&user)

	secret := "supersecret"

	// Test successful login
	token, u, err := loginPassword(db, ns.ID, "testuser", "password123", secret)
	if err != nil {
		t.Fatalf("login failed: %v", err)
	}
	if u.ID != user.ID {
		t.Errorf("expected user ID %s, got %s", user.ID, u.ID)
	}
	if token == "" {
		t.Error("expected non-empty token")
	}

	// Test invalid password
	_, _, err = loginPassword(db, ns.ID, "testuser", "wrong", secret)
	if err == nil {
		t.Error("expected error for wrong password")
	}

	// Parse JWT
	claims, err := parseJWT(token, secret)
	if err != nil {
		t.Fatalf("failed to parse JWT: %v", err)
	}
	if claims.UserID != user.ID {
		t.Errorf("expected JWT UserID %s, got %s", user.ID, claims.UserID)
	}

	// Revoke Session
	err = revokeSession(db, claims.ID)
	if err != nil {
		t.Fatalf("failed to revoke session: %v", err)
	}

	// Validate Session
	_, err = validateSession(db, claims.ID)
	if err == nil || err.Error() != "session revoked" {
		t.Errorf("expected session revoked error, got %v", err)
	}
}

func TestUpdateOps(t *testing.T) {
	// Let's test applyUpdateOps standalone
	target := map[string]any{
		"score":   float64(10),
		"tags":    []interface{}{"A", "B"},
		"profile": map[string]any{"age": 20},
	}

	patch := map[string]any{
		"score":   map[string]any{"$inc": float64(5)},
		"tags":    map[string]any{"$push": "C", "$pull": "A"},
		"profile": map[string]any{"isActive": true}, // implicit replace
		"deleted": map[string]any{"$unset": true},
	}
	target["deleted"] = "yes"

	applyUpdateOps(target, patch)

	if target["score"] != float64(15) {
		t.Errorf("expected score 15, got %v", target["score"])
	}

	tags, ok := target["tags"].([]interface{})
	if !ok {
		t.Fatalf("tags is not an array")
	}
	if len(tags) != 2 || tags[0] != "B" || tags[1] != "C" {
		t.Errorf("expected tags [B, C], got %v", tags)
	}

	if _, ok := target["deleted"]; ok {
		t.Errorf("expected deleted key to be unset")
	}

	prof, _ := target["profile"].(map[string]any)
	if val, ok := prof["isActive"]; !ok || val != true {
		t.Errorf("expected profile to be replaced with isActive, got %v", prof)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// $sql operator
// ─────────────────────────────────────────────────────────────────────────────

func TestFilter_SQL_String(t *testing.T) {
	db := setupTestDB(t)
	var ns Namespace
	db.Where("slug = ?", "global").First(&ns)

	s := CrudSchema{ID: newID(), Name: "Items", Slug: "sql_items", NamespaceID: ns.ID, SoftDelete: false, AllowRawSQL: true, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	db.Create(&s)
	col, _ := newCollection(db, &s, nil, "")

	col.Create(map[string]any{"price": 10.0, "stock": 5.0}, nil)
	col.Create(map[string]any{"price": 200.0, "stock": 3.0}, nil)
	col.Create(map[string]any{"price": 50.0, "stock": 0.0}, nil)

	// Raw SQL string — find docs where both price and stock are positive
	docs, err := col.Find(map[string]any{
		"$sql": "json_extract(data,'$.price') > 0 AND json_extract(data,'$.stock') > 0",
	}, ListOptions{Limit: 10})
	if err != nil {
		t.Fatalf("$sql string: %v", err)
	}
	if len(docs) != 2 {
		t.Errorf("expected 2 docs with price>0 AND stock>0, got %d", len(docs))
	}
}

func TestFilter_SQL_ExprArgs(t *testing.T) {
	db := setupTestDB(t)
	var ns Namespace
	db.Where("slug = ?", "global").First(&ns)

	s := CrudSchema{ID: newID(), Name: "Items2", Slug: "sql_items2", NamespaceID: ns.ID, SoftDelete: false, AllowRawSQL: true, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	db.Create(&s)
	col, _ := newCollection(db, &s, nil, "")

	col.Create(map[string]any{"price": 120.0}, nil)
	col.Create(map[string]any{"price": 80.0}, nil)

	// Parameterised $sql — safe from injection
	docs, err := col.Find(map[string]any{
		"$sql": map[string]any{
			"expr": "json_extract(data,'$.price') > ?",
			"args": []interface{}{100.0},
		},
	}, ListOptions{Limit: 10})
	if err != nil {
		t.Fatalf("$sql expr+args: %v", err)
	}
	if len(docs) != 1 {
		t.Errorf("expected 1 doc with price>100, got %d", len(docs))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// OLC decoder
// ─────────────────────────────────────────────────────────────────────────────

func TestDecodeOLC_Valid(t *testing.T) {
	// Paris, France
	code := "8FW4V8F2+GX"
	pt, err := decodeOLC(code)
	if err != nil {
		t.Fatalf("decodeOLC(%q): %v", code, err)
	}
	// Paris: lon ≈ 2.35, lat ≈ 48.85
	if pt[0] < 2.0 || pt[0] > 3.0 {
		t.Errorf("longitude out of range: %v", pt[0])
	}
	if pt[1] < 48.0 || pt[1] > 49.0 {
		t.Errorf("latitude out of range: %v", pt[1])
	}
}

func TestDecodeOLC_Short_With_Loc_Ref(t *testing.T) {
	// Paris, France
	code := "V8F2+GX"
	pt, err := decodeOLC(code, 2.3522, 48.8566)
	if err != nil {
		t.Fatalf("decodeOLC(%q): %v", code, err)
	}
	// Paris: lon ≈ 2.30, lat ≈ 48.87
	if pt[0] < 2.0 || pt[0] > 3.0 {
		t.Errorf("longitude out of range: %v", pt[0])
	}
	if pt[1] < 48.0 || pt[1] > 49.0 {
		t.Errorf("latitude out of range: %v", pt[1])
	}
}

func TestDecodeOLC_Missing_Separator(t *testing.T) {
	if _, err := decodeOLC("ABCDEF1234"); err == nil {
		t.Error("expected error for code without '+'")
	}
}

func TestDecodeOLC_TooShort(t *testing.T) {
	if _, err := decodeOLC("AB+"); err == nil {
		t.Error("expected error for code too short")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// geoFieldExpr
// ─────────────────────────────────────────────────────────────────────────────

func TestGeoFieldExpr_GeoColumn(t *testing.T) {
	lng, lat := geoFieldExpr("geo")
	if !strings.Contains(lng, "geo") {
		t.Errorf("expected geo column expr, got %q", lng)
	}
	if !strings.Contains(lat, "geo") {
		t.Errorf("expected geo column expr, got %q", lat)
	}
}

func TestGeoFieldExpr_DataField(t *testing.T) {
	lng, lat := geoFieldExpr("location")
	if !strings.Contains(lng, "location") {
		t.Errorf("expected location in lng expr, got %q", lng)
	}
	if !strings.Contains(lat, "location") {
		t.Errorf("expected location in lat expr, got %q", lat)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// extractGeoCenter
// ─────────────────────────────────────────────────────────────────────────────

func TestExtractGeoCenter_Geometry(t *testing.T) {
	m := map[string]interface{}{
		"$geometry": map[string]interface{}{
			"type":        "Point",
			"coordinates": []interface{}{11.52, 3.87},
		},
		"$maxDistance": 5000.0,
	}
	lng, lat, ok := extractGeoCenter(m)
	if !ok || lng != 11.52 || lat != 3.87 {
		t.Errorf("got lng=%v lat=%v ok=%v", lng, lat, ok)
	}
}

func TestExtractGeoCenter_Center(t *testing.T) {
	m := map[string]interface{}{
		"$center":      []interface{}{2.35, 48.85},
		"$maxDistance": 1000.0,
	}
	lng, lat, ok := extractGeoCenter(m)
	if !ok || lng != 2.35 || lat != 48.85 {
		t.Errorf("got lng=%v lat=%v ok=%v", lng, lat, ok)
	}
}

func TestExtractGeoCenter_OLC(t *testing.T) {
	m := map[string]interface{}{
		"$olc":         "8FW4V75V+8Q",
		"$maxDistance": 500.0,
	}
	lng, lat, ok := extractGeoCenter(m)
	if !ok {
		t.Fatal("expected ok=true for valid OLC")
	}
	if lng < 2.0 || lng > 3.0 {
		t.Errorf("lng out of range: %v", lng)
	}
	if lat < 48.0 || lat > 49.0 {
		t.Errorf("lat out of range: %v", lat)
	}
}

func TestExtractGeoCenter_Missing(t *testing.T) {
	_, _, ok := extractGeoCenter(map[string]interface{}{"$maxDistance": 1000.0})
	if ok {
		t.Error("expected ok=false for map without geo source")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// buildWithinExpr
// ─────────────────────────────────────────────────────────────────────────────

func TestBuildWithinExpr_Box(t *testing.T) {
	m := map[string]interface{}{
		"$box": []interface{}{
			[]interface{}{-10.0, 35.0},
			[]interface{}{30.0, 60.0},
		},
	}
	expr, args := buildWithinExpr(m, "lng_col", "lat_col")
	if expr == "" {
		t.Fatal("expected non-empty expr for $box")
	}
	if len(args) != 4 {
		t.Errorf("expected 4 args, got %d", len(args))
	}
	if args[0] != -10.0 || args[1] != 30.0 || args[2] != 35.0 || args[3] != 60.0 {
		t.Errorf("unexpected args: %v", args)
	}
}

func TestBuildWithinExpr_Center(t *testing.T) {
	m := map[string]interface{}{
		"$center": []interface{}{2.35, 48.85},
		"$radius": 1.0,
	}
	expr, args := buildWithinExpr(m, "lng_col", "lat_col")
	if expr == "" {
		t.Fatal("expected non-empty expr for $center+$radius")
	}
	if len(args) != 6 {
		t.Errorf("expected 6 args (lng,lng,lat,lat,r,r), got %d", len(args))
	}
}

func TestBuildWithinExpr_Geometry(t *testing.T) {
	m := map[string]interface{}{
		"$geometry": map[string]interface{}{
			"type": "Polygon",
			"coordinates": []interface{}{[]interface{}{
				[]interface{}{-10.0, 35.0},
				[]interface{}{30.0, 35.0},
				[]interface{}{30.0, 60.0},
				[]interface{}{-10.0, 60.0},
				[]interface{}{-10.0, 35.0},
			}},
		},
	}
	expr, args := buildWithinExpr(m, "lng_col", "lat_col")
	if expr == "" {
		t.Fatal("expected non-empty expr for $geometry Polygon")
	}
	if len(args) != 4 {
		t.Errorf("expected 4 bounding-box args, got %d", len(args))
	}
}

func TestBuildWithinExpr_Empty(t *testing.T) {
	expr, _ := buildWithinExpr(map[string]interface{}{}, "lng", "lat")
	if expr != "" {
		t.Errorf("expected empty expr for empty spec, got %q", expr)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// $near / $within / $without in applyMongoFilter (integration with DB)
// ─────────────────────────────────────────────────────────────────────────────

// seedGeoDoc inserts a document whose `location` field is a [lng,lat] array.
func seedGeoDoc(t *testing.T, db *gorm.DB, schemaID, nsID, name string, lng, lat float64) string {
	t.Helper()
	data, _ := json.Marshal(map[string]any{
		"name":     name,
		"location": []float64{lng, lat},
	})
	doc := &CrudDocument{
		ID:          newID(),
		SchemaID:    schemaID,
		NamespaceID: nsID,
		Data:        string(data),
		Meta:        "{}",
		Geo:         "",
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := db.Create(doc).Error; err != nil {
		t.Fatalf("seedGeoDoc: %v", err)
	}
	return doc.ID
}

func setupGeoSchema(t *testing.T, db *gorm.DB, nsID, slug string) *CrudSchema {
	t.Helper()
	s := &CrudSchema{
		ID:          newID(),
		Name:        slug,
		Slug:        slug,
		NamespaceID: nsID,
		SoftDelete:  false,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	db.Create(s)
	return s
}

func TestFilter_Near_Geometry(t *testing.T) {
	db := setupTestDB(t)
	var ns Namespace
	db.Where("slug = ?", "global").First(&ns)
	s := setupGeoSchema(t, db, ns.ID, "geo_near_geom")

	// Paris [2.35, 48.85], London [-0.12, 51.50], Tokyo [139.69, 35.69]
	seedGeoDoc(t, db, s.ID, ns.ID, "Paris", 2.35, 48.85)
	seedGeoDoc(t, db, s.ID, ns.ID, "London", -0.12, 51.50)
	seedGeoDoc(t, db, s.ID, ns.ID, "Tokyo", 139.69, 35.69)

	col, _ := newCollection(db, s, nil, "")

	// Near Paris, 500 km
	docs, err := col.Find(map[string]any{
		"location": map[string]any{
			"$near": map[string]any{
				"$geometry":    map[string]any{"type": "Point", "coordinates": []interface{}{2.35, 48.85}},
				"$maxDistance": 500_000.0,
			},
		},
	}, ListOptions{Limit: 10})
	if err != nil {
		t.Fatalf("$near geometry: %v", err)
	}
	if len(docs) == 0 {
		t.Fatal("expected at least Paris near itself")
	}
	if docs[0]["name"] != "Paris" {
		t.Errorf("first result should be Paris, got %v", docs[0]["name"])
	}
}

func TestFilter_Near_Center(t *testing.T) {
	db := setupTestDB(t)
	var ns Namespace
	db.Where("slug = ?", "global").First(&ns)
	s := setupGeoSchema(t, db, ns.ID, "geo_near_center")

	seedGeoDoc(t, db, s.ID, ns.ID, "Paris", 2.35, 48.85)
	seedGeoDoc(t, db, s.ID, ns.ID, "Tokyo", 139.69, 35.69)

	col, _ := newCollection(db, s, nil, "")

	docs, err := col.Find(map[string]any{
		"location": map[string]any{
			"$near": map[string]any{
				"$center":      []interface{}{2.35, 48.85},
				"$maxDistance": 200_000.0,
			},
		},
	}, ListOptions{Limit: 10})
	if err != nil {
		t.Fatalf("$near center: %v", err)
	}
	if len(docs) != 1 || docs[0]["name"] != "Paris" {
		t.Errorf("expected only Paris near Paris (200km), got %v", docs)
	}
}

func TestFilter_Near_OLC(t *testing.T) {
	db := setupTestDB(t)
	var ns Namespace
	db.Where("slug = ?", "global").First(&ns)
	s := setupGeoSchema(t, db, ns.ID, "geo_near_olc")

	// Paris area
	seedGeoDoc(t, db, s.ID, ns.ID, "Paris", 2.35, 48.85)
	seedGeoDoc(t, db, s.ID, ns.ID, "London", -0.12, 51.50)

	col, _ := newCollection(db, s, nil, "")

	// OLC for Paris centre
	docs, err := col.Find(map[string]any{
		"location": map[string]any{
			"$near": map[string]any{
				"$olc":         "8FW4V75V+8Q",
				"$maxDistance": 50_000.0, // 50 km
			},
		},
	}, ListOptions{Limit: 10})
	if err != nil {
		t.Fatalf("$near OLC: %v", err)
	}
	if len(docs) == 0 {
		t.Error("expected at least Paris near its own OLC")
	}
}

func TestFilter_Within_Box(t *testing.T) {
	db := setupTestDB(t)
	var ns Namespace
	db.Where("slug = ?", "global").First(&ns)
	s := setupGeoSchema(t, db, ns.ID, "geo_within_box")

	seedGeoDoc(t, db, s.ID, ns.ID, "Paris", 2.35, 48.85)
	seedGeoDoc(t, db, s.ID, ns.ID, "Berlin", 13.40, 52.52)
	seedGeoDoc(t, db, s.ID, ns.ID, "New York", -74.00, 40.71)

	col, _ := newCollection(db, s, nil, "")

	docs, err := col.Find(map[string]any{
		"location": map[string]any{
			"$within": map[string]any{
				"$box": []interface{}{
					[]interface{}{-15.0, 35.0}, // SW corner
					[]interface{}{35.0, 60.0},  // NE corner
				},
			},
		},
	}, ListOptions{Limit: 10})
	if err != nil {
		t.Fatalf("$within box: %v", err)
	}
	if len(docs) != 2 {
		t.Errorf("expected 2 European cities in box, got %d: %v", len(docs), docs)
	}
}

func TestFilter_Within_Geometry(t *testing.T) {
	db := setupTestDB(t)
	var ns Namespace
	db.Where("slug = ?", "global").First(&ns)
	s := setupGeoSchema(t, db, ns.ID, "geo_within_geom")

	seedGeoDoc(t, db, s.ID, ns.ID, "Paris", 2.35, 48.85)
	seedGeoDoc(t, db, s.ID, ns.ID, "Tokyo", 139.69, 35.69)

	col, _ := newCollection(db, s, nil, "")

	docs, err := col.Find(map[string]any{
		"location": map[string]any{
			"$within": map[string]any{
				"$geometry": map[string]any{
					"type": "Polygon",
					"coordinates": []interface{}{[]interface{}{
						[]interface{}{-15.0, 35.0},
						[]interface{}{35.0, 35.0},
						[]interface{}{35.0, 60.0},
						[]interface{}{-15.0, 60.0},
						[]interface{}{-15.0, 35.0},
					}},
				},
			},
		},
	}, ListOptions{Limit: 10})
	if err != nil {
		t.Fatalf("$within geometry: %v", err)
	}
	if len(docs) != 1 || docs[0]["name"] != "Paris" {
		t.Errorf("expected only Paris in Europe polygon, got %v", docs)
	}
}

func TestFilter_Without_Box(t *testing.T) {
	db := setupTestDB(t)
	var ns Namespace
	db.Where("slug = ?", "global").First(&ns)
	s := setupGeoSchema(t, db, ns.ID, "geo_without_box")

	seedGeoDoc(t, db, s.ID, ns.ID, "Paris", 2.35, 48.85)
	seedGeoDoc(t, db, s.ID, ns.ID, "Berlin", 13.40, 52.52)
	seedGeoDoc(t, db, s.ID, ns.ID, "New York", -74.00, 40.71)

	col, _ := newCollection(db, s, nil, "")

	// Exclude Europe box → only New York
	docs, err := col.Find(map[string]any{
		"location": map[string]any{
			"$without": map[string]any{
				"$box": []interface{}{
					[]interface{}{-15.0, 35.0},
					[]interface{}{35.0, 60.0},
				},
			},
		},
	}, ListOptions{Limit: 10})
	if err != nil {
		t.Fatalf("$without box: %v", err)
	}
	if len(docs) != 1 || docs[0]["name"] != "New York" {
		t.Errorf("expected only New York outside Europe, got %v", docs)
	}
}

func TestFilter_Near_GeoColumn(t *testing.T) {
	db := setupTestDB(t)
	var ns Namespace
	db.Where("slug = ?", "global").First(&ns)
	s := setupGeoSchema(t, db, ns.ID, "geo_col_near")

	// Insert using the dedicated geo column
	geoDoc := func(name string, lng, lat float64) {
		geoJSON, _ := json.Marshal(map[string]any{
			"type":        "Point",
			"coordinates": []float64{lng, lat},
		})
		dataJSON, _ := json.Marshal(map[string]any{"name": name})
		doc := &CrudDocument{
			ID: newID(), SchemaID: s.ID, NamespaceID: ns.ID,
			Data: string(dataJSON), Meta: "{}", Geo: string(geoJSON),
			CreatedAt: time.Now(), UpdatedAt: time.Now(),
		}
		db.Create(doc)
	}
	geoDoc("Paris", 2.35, 48.85)
	geoDoc("London", -0.12, 51.50)
	geoDoc("Tokyo", 139.69, 35.69)

	col, _ := newCollection(db, s, nil, "")

	// Filter on the dedicated "geo" field
	docs, err := col.Find(map[string]any{
		"geo": map[string]any{
			"$near": map[string]any{
				"$center":      []interface{}{2.35, 48.85},
				"$maxDistance": 300_000.0,
			},
		},
	}, ListOptions{Limit: 10})
	if err != nil {
		t.Fatalf("$near on geo column: %v", err)
	}
	if len(docs) == 0 {
		t.Error("expected Paris near itself via geo column")
	}
	if docs[0]["name"] != "Paris" {
		t.Errorf("first result should be Paris, got %v", docs[0]["name"])
	}
}

func TestFilter_Within_Center(t *testing.T) {
	db := setupTestDB(t)
	var ns Namespace
	db.Where("slug = ?", "global").First(&ns)
	s := setupGeoSchema(t, db, ns.ID, "geo_within_center")

	seedGeoDoc(t, db, s.ID, ns.ID, "Near", 2.4, 48.9)
	seedGeoDoc(t, db, s.ID, ns.ID, "Far", 10.0, 55.0)

	col, _ := newCollection(db, s, nil, "")

	docs, err := col.Find(map[string]any{
		"location": map[string]any{
			"$within": map[string]any{
				"$center": []interface{}{2.35, 48.85},
				"$radius": 1.0, // 1 degree ≈ 111 km
			},
		},
	}, ListOptions{Limit: 10})
	if err != nil {
		t.Fatalf("$within center+radius: %v", err)
	}
	if len(docs) != 1 || docs[0]["name"] != "Near" {
		t.Errorf("expected only Near point in circle, got %v", docs)
	}
}

func TestFilter_Geo_NumericTypes(t *testing.T) {
	db := setupTestDB(t)
	var ns Namespace
	db.Where("slug = ?", "global").First(&ns)
	s := setupGeoSchema(t, db, ns.ID, "geo_num_types")

	// Paris as float64
	seedGeoDoc(t, db, s.ID, ns.ID, "Paris-Float", 2.35, 48.85)

	// Custom insert with int/uint (simulated via JSON)
	insertDoc := func(name string, lng, lat interface{}) {
		dataJSON, _ := json.Marshal(map[string]any{
			"name":     name,
			"location": []interface{}{lng, lat},
		})
		doc := &CrudDocument{
			ID: newID(), SchemaID: s.ID, NamespaceID: ns.ID,
			Data: string(dataJSON), Meta: "{}",
			CreatedAt: time.Now(), UpdatedAt: time.Now(),
		}
		db.Create(doc)
	}

	insertDoc("Paris-Int", 2, 48)              // Integers
	insertDoc("Paris-Uint", uint(2), uint(48)) // Uint (encoded as number in JSON)

	col, _ := newCollection(db, s, nil, "")

	// Test $near with center in box [0,0] to [10,60]
	docs, err := col.Find(map[string]any{
		"location": map[string]any{
			"$within": map[string]any{
				"$box": []interface{}{
					[]interface{}{0, 0},
					[]interface{}{10, 60},
				},
			},
		},
	}, ListOptions{Limit: 10})

	if err != nil {
		t.Fatalf("$within numeric: %v", err)
	}
	if len(docs) != 3 {
		t.Errorf("expected 3 docs (Float, Int, Uint), got %d: %v", len(docs), docs)
	}
}
