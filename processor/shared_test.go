package processor

// shared_test.go — Tests de SharedObject : proxy ES6 Go↔JS via goja.
//
// Chaque test crée un SharedObject isolé (non enregistré dans sharedObjects),
// l'installe dans une VM fraîche et vérifie les lectures/écritures Go↔JS.
//
// Helpers :
//   run(t, so, script)          → exécute le script, retourne la dernière valeur
//   runInVM(t, so, vm, script)  → réutilise une VM existante
//   mustBool / mustInt / mustStr / mustFloat → assertions de valeur

import (
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/dop251/goja"
)

// ==================== HELPERS ====================

func newSO(name string, data interface{}) *SharedObject {
	return &SharedObject{name: name, data: data}
}

func run(t *testing.T, so *SharedObject, script string) goja.Value {
	t.Helper()
	vm := NewEmpty()
	vm.AttachGlobals()
	if err := so.attach(vm.Runtime); err != nil {
		t.Fatalf("attach: %v", err)
	}
	val, err := vm.RunString(script)
	if err != nil {
		t.Fatalf("RunString: %v", err)
	}
	return val
}

func newVM(t *testing.T, so *SharedObject) *goja.Runtime {
	t.Helper()
	vm := NewEmpty()
	vm.AttachGlobals()
	if err := so.attach(vm.Runtime); err != nil {
		t.Fatalf("attach: %v", err)
	}
	return vm.Runtime
}

func runInVM(t *testing.T, vm *goja.Runtime, script string) goja.Value {
	t.Helper()
	val, err := vm.RunString(script)
	if err != nil {
		t.Fatalf("RunString: %v", err)
	}
	return val
}

func mustBool(t *testing.T, v goja.Value, want bool) {
	t.Helper()
	if got := v.ToBoolean(); got != want {
		t.Errorf("expected bool %v, got %v", want, got)
	}
}

func mustInt(t *testing.T, v goja.Value, want int64) {
	t.Helper()
	if got := v.ToInteger(); got != want {
		t.Errorf("expected int %d, got %d", want, got)
	}
}

func mustStr(t *testing.T, v goja.Value, want string) {
	t.Helper()
	if got := v.String(); got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func mustFloat(t *testing.T, v goja.Value, want float64) {
	t.Helper()
	if got := v.ToFloat(); got != want {
		t.Errorf("expected float %f, got %f", want, got)
	}
}

// ==================== FIXTURES ====================

type Address struct {
	Street string
	City   string
	Zip    int
}

type TLSConfig struct {
	CertFile string `json:"certFile"`
	KeyFile  string `json:"keyFile"`
	Enabled  bool
}

type DBConfig struct {
	Host    string
	Port    int
	Replica *Address
}

type ServerConfig struct {
	Host     string
	Port     int
	Debug    bool
	Pi       float64
	Score    float32
	Ratio    float32
	Byte     uint8
	Rune     rune
	Count    uint64
	Tls      TLSConfig
	DB       DBConfig
	Tags     []string
	Ports    []int
	Weights  []float64
	Env      map[string]string
	Counters map[string]int
	IntMap   map[int]string
	Nested   *Address
	NilPtr   *Address
	NilSlice []string
	NilMap   map[string]int
}

// ==================== STRUCT — LECTURE ====================

func TestStruct_ReadString(t *testing.T) {
	mustStr(t, run(t, newSO("Config", &ServerConfig{Host: "example.com"}), `config.host`), "example.com")
}

func TestStruct_ReadInt(t *testing.T) {
	mustInt(t, run(t, newSO("Config", &ServerConfig{Port: 8080}), `config.port`), 8080)
}

func TestStruct_ReadBool(t *testing.T) {
	mustBool(t, run(t, newSO("Config", &ServerConfig{Debug: true}), `config.debug`), true)
}

func TestStruct_ReadFloat64(t *testing.T) {
	mustFloat(t, run(t, newSO("Config", &ServerConfig{Pi: 3.14159}), `config.pi`), 3.14159)
}

func TestStruct_ReadFloat32(t *testing.T) {
	v := run(t, newSO("Config", &ServerConfig{Score: 9.5}), `config.score`).ToFloat()
	if v < 9.49 || v > 9.51 {
		t.Errorf("expected ~9.5, got %f", v)
	}
}

func TestStruct_ReadUint8(t *testing.T) {
	mustInt(t, run(t, newSO("Config", &ServerConfig{Byte: 255}), `config.byte`), 255)
}

func TestStruct_ReadRune(t *testing.T) {
	mustInt(t, run(t, newSO("Config", &ServerConfig{Rune: '€'}), `config.rune`), int64('€'))
}

func TestStruct_ReadUint64(t *testing.T) {
	mustInt(t, run(t, newSO("Config", &ServerConfig{Count: 1_000_000}), `config.count`), 1_000_000)
}

func TestStruct_UnknownField_Undefined(t *testing.T) {
	mustBool(t, run(t, newSO("Config", &ServerConfig{}), `config.doesNotExist === undefined`), true)
}

func TestStruct_HasField_True(t *testing.T) {
	mustBool(t, run(t, newSO("Config", &ServerConfig{}), `"host" in config`), true)
}

func TestStruct_HasField_False(t *testing.T) {
	mustBool(t, run(t, newSO("Config", &ServerConfig{}), `"ghost" in config`), false)
}

func TestStruct_ObjectKeys_ContainsFields(t *testing.T) {
	so := newSO("Config", &ServerConfig{})
	mustBool(t, run(t, so, `Object.keys(config).includes("host")`), true)
	mustBool(t, run(t, so, `Object.keys(config).includes("port")`), true)
}

func TestStruct_ObjectKeys_ContainsSubscribeFns(t *testing.T) {
	// subscribe et unsubscribe sont visibles dans Object.keys
	so := newSO("Config", &ServerConfig{})
	mustBool(t, run(t, so, `Object.keys(config).includes("subscribe")`), true)
	mustBool(t, run(t, so, `Object.keys(config).includes("unsubscribe")`), true)
}

// ==================== STRUCT — ÉCRITURE ====================

func TestStruct_WriteString(t *testing.T) {
	cfg := &ServerConfig{Host: "old"}
	run(t, newSO("Config", cfg), `config.host = "new"`)
	if cfg.Host != "new" {
		t.Errorf("expected 'new', got %q", cfg.Host)
	}
}

func TestStruct_WriteInt(t *testing.T) {
	cfg := &ServerConfig{Port: 80}
	run(t, newSO("Config", cfg), `config.port = 443`)
	if cfg.Port != 443 {
		t.Errorf("expected 443, got %d", cfg.Port)
	}
}

func TestStruct_WriteBool(t *testing.T) {
	cfg := &ServerConfig{Debug: false}
	run(t, newSO("Config", cfg), `config.debug = true`)
	if !cfg.Debug {
		t.Error("expected Debug=true")
	}
}

func TestStruct_WriteFloat64(t *testing.T) {
	cfg := &ServerConfig{}
	run(t, newSO("Config", cfg), `config.pi = 2.71828`)
	if cfg.Pi < 2.71 || cfg.Pi > 2.72 {
		t.Errorf("expected ~2.71828, got %f", cfg.Pi)
	}
}

func TestStruct_WriteUint8(t *testing.T) {
	cfg := &ServerConfig{}
	run(t, newSO("Config", cfg), `config.byte = 42`)
	if cfg.Byte != 42 {
		t.Errorf("expected 42, got %d", cfg.Byte)
	}
}

func TestStruct_WriteRune(t *testing.T) {
	cfg := &ServerConfig{}
	run(t, newSO("Config", cfg), `config.rune = 65`)
	if cfg.Rune != 65 {
		t.Errorf("expected 65 ('A'), got %d", cfg.Rune)
	}
}

func TestStruct_WriteLiveInSameVM(t *testing.T) {
	cfg := &ServerConfig{Port: 80}
	mustBool(t, run(t, newSO("Config", cfg), `config.port = 9090; config.port === 9090;`), true)
	if cfg.Port != 9090 {
		t.Errorf("Go not updated, got %d", cfg.Port)
	}
}

func TestStruct_CannotDeleteField(t *testing.T) {
	cfg := &ServerConfig{Host: "x"}
	run(t, newSO("Config", cfg), `delete config.host`)
	if cfg.Host != "x" {
		t.Error("struct field should not be deletable")
	}
}

func TestStruct_SubscribeNotOverwritable(t *testing.T) {
	// subscribe ne peut pas être écrasé
	so := newSO("Config", &ServerConfig{})
	mustBool(t, run(t, so, `config.subscribe = "nope"; typeof config.subscribe === "function"`), true)
}

// ==================== JSON TAG ====================

func TestJsonTag_Read(t *testing.T) {
	cfg := &ServerConfig{}
	cfg.Tls.CertFile = "/etc/cert.pem"
	mustStr(t, run(t, newSO("Config", cfg), `config.tls.certFile`), "/etc/cert.pem")
}

func TestJsonTag_Write(t *testing.T) {
	cfg := &ServerConfig{}
	run(t, newSO("Config", cfg), `config.tls.certFile = "/new.pem"`)
	if cfg.Tls.CertFile != "/new.pem" {
		t.Errorf("got %q", cfg.Tls.CertFile)
	}
}

// ==================== STRUCT IMBRIQUÉE ====================

func TestNested_Read(t *testing.T) {
	cfg := &ServerConfig{}
	cfg.Tls.CertFile = "/cert.pem"
	cfg.Tls.Enabled = true
	so := newSO("Config", cfg)
	mustStr(t, run(t, so, `config.tls.certFile`), "/cert.pem")
	mustBool(t, run(t, so, `config.tls.enabled`), true)
}

func TestNested_Write(t *testing.T) {
	cfg := &ServerConfig{}
	run(t, newSO("Config", cfg), `config.tls.keyFile = "/key.pem"`)
	if cfg.Tls.KeyFile != "/key.pem" {
		t.Errorf("got %q", cfg.Tls.KeyFile)
	}
}

func TestNested_ThreeLevels_Read(t *testing.T) {
	cfg := &ServerConfig{}
	cfg.DB.Replica = &Address{City: "Lyon"}
	mustStr(t, run(t, newSO("Config", cfg), `config.dB.replica.city`), "Lyon")
}

func TestNested_ThreeLevels_Write(t *testing.T) {
	cfg := &ServerConfig{}
	cfg.DB.Replica = &Address{City: "Lyon"}
	run(t, newSO("Config", cfg), `config.dB.replica.city = "Paris"`)
	if cfg.DB.Replica.City != "Paris" {
		t.Errorf("got %q", cfg.DB.Replica.City)
	}
}

func TestNestedPtr_Nil_IsNull(t *testing.T) {
	mustBool(t, run(t, newSO("Config", &ServerConfig{NilPtr: nil}), `config.nilPtr === null`), true)
}

func TestNestedPtr_NonNil_Read(t *testing.T) {
	cfg := &ServerConfig{Nested: &Address{Street: "Rue de la Paix", City: "Paris"}}
	so := newSO("Config", cfg)
	mustStr(t, run(t, so, `config.nested.street`), "Rue de la Paix")
	mustStr(t, run(t, so, `config.nested.city`), "Paris")
}

func TestNestedPtr_NonNil_Write(t *testing.T) {
	cfg := &ServerConfig{Nested: &Address{City: "Lyon"}}
	run(t, newSO("Config", cfg), `config.nested.city = "Marseille"`)
	if cfg.Nested.City != "Marseille" {
		t.Errorf("got %q", cfg.Nested.City)
	}
}

// ==================== SLICE ====================

func TestSlice_Length(t *testing.T) {
	mustInt(t, run(t, newSO("Config", &ServerConfig{Tags: []string{"a", "b", "c"}}), `config.tags.length`), 3)
}

func TestSlice_ReadIndex(t *testing.T) {
	so := newSO("Config", &ServerConfig{Tags: []string{"alpha", "beta"}})
	mustStr(t, run(t, so, `config.tags[0]`), "alpha")
	mustStr(t, run(t, so, `config.tags[1]`), "beta")
}

func TestSlice_OutOfBounds_Undefined(t *testing.T) {
	mustBool(t, run(t, newSO("Config", &ServerConfig{Tags: []string{"x"}}), `config.tags[99] === undefined`), true)
}

func TestSlice_WriteString(t *testing.T) {
	cfg := &ServerConfig{Tags: []string{"old", "keep"}}
	run(t, newSO("Config", cfg), `config.tags[0] = "new"`)
	if cfg.Tags[0] != "new" || cfg.Tags[1] != "keep" {
		t.Errorf("got %v", cfg.Tags)
	}
}

func TestSlice_WriteInt(t *testing.T) {
	cfg := &ServerConfig{Ports: []int{80, 443}}
	run(t, newSO("Config", cfg), `config.ports[0] = 8080`)
	if cfg.Ports[0] != 8080 {
		t.Errorf("expected 8080, got %d", cfg.Ports[0])
	}
}

func TestSlice_WriteFloat(t *testing.T) {
	cfg := &ServerConfig{Weights: []float64{1.0, 2.0}}
	run(t, newSO("Config", cfg), `config.weights[1] = 3.14`)
	if cfg.Weights[1] < 3.13 || cfg.Weights[1] > 3.15 {
		t.Errorf("expected ~3.14, got %f", cfg.Weights[1])
	}
}

func TestSlice_Nil_LengthZero(t *testing.T) {
	mustInt(t, run(t, newSO("Config", &ServerConfig{NilSlice: nil}), `config.nilSlice.length`), 0)
}

func TestSlice_Nil_NotNull(t *testing.T) {
	mustBool(t, run(t, newSO("Config", &ServerConfig{NilSlice: nil}), `config.nilSlice !== null`), true)
}

func TestSlice_Nil_IndexUndefined(t *testing.T) {
	mustBool(t, run(t, newSO("Config", &ServerConfig{NilSlice: nil}), `config.nilSlice[0] === undefined`), true)
}

func TestSlice_Iteration(t *testing.T) {
	so := newSO("Config", &ServerConfig{Tags: []string{"a", "bb", "ccc"}})
	mustInt(t, run(t, so, `
		var s = 0;
		for (var i = 0; i < config.tags.length; i++) { s += config.tags[i].length; }
		s;
	`), 6)
}

func TestSlice_LiveAfterGoMutation(t *testing.T) {
	cfg := &ServerConfig{Tags: []string{"v1"}}
	so := newSO("Config", cfg)
	mustStr(t, run(t, so, `config.tags[0]`), "v1")
	cfg.Tags[0] = "v2"
	mustStr(t, run(t, so, `config.tags[0]`), "v2")
}

// ==================== MAP ====================

func TestMap_ReadKey(t *testing.T) {
	mustStr(t, run(t, newSO("Config", &ServerConfig{Env: map[string]string{"KEY": "val"}}), `config.env["KEY"]`), "val")
}

func TestMap_MissingKey_Undefined(t *testing.T) {
	mustBool(t, run(t, newSO("Config", &ServerConfig{Env: map[string]string{}}), `config.env["NOPE"] === undefined`), true)
}

func TestMap_WriteKey(t *testing.T) {
	cfg := &ServerConfig{Env: map[string]string{}}
	run(t, newSO("Config", cfg), `config.env["K"] = "v"`)
	if cfg.Env["K"] != "v" {
		t.Errorf("got %q", cfg.Env["K"])
	}
}

func TestMap_OverwriteKey(t *testing.T) {
	cfg := &ServerConfig{Env: map[string]string{"X": "old"}}
	run(t, newSO("Config", cfg), `config.env["X"] = "new"`)
	if cfg.Env["X"] != "new" {
		t.Errorf("got %q", cfg.Env["X"])
	}
}

func TestMap_DeleteKey(t *testing.T) {
	cfg := &ServerConfig{Env: map[string]string{"A": "1", "B": "2"}}
	run(t, newSO("Config", cfg), `delete config.env["A"]`)
	if _, ok := cfg.Env["A"]; ok {
		t.Error("key A should be deleted")
	}
	if cfg.Env["B"] != "2" {
		t.Error("key B should still exist")
	}
}

func TestMap_HasKey_True(t *testing.T) {
	mustBool(t, run(t, newSO("Config", &ServerConfig{Env: map[string]string{"X": "1"}}), `"X" in config.env`), true)
}

func TestMap_HasKey_False(t *testing.T) {
	mustBool(t, run(t, newSO("Config", &ServerConfig{Env: map[string]string{}}), `"Y" in config.env`), false)
}

func TestMap_ObjectKeys_Count(t *testing.T) {
	so := newSO("Config", &ServerConfig{Env: map[string]string{"A": "1", "B": "2"}})
	mustInt(t, run(t, so, `Object.keys(config.env).length`), 2)
}

func TestMap_IntKey_Read(t *testing.T) {
	so := newSO("Config", &ServerConfig{IntMap: map[int]string{1: "one", 2: "two"}})
	mustStr(t, run(t, so, `config.intMap["1"]`), "one")
}

func TestMap_IntValues_Read(t *testing.T) {
	mustInt(t, run(t, newSO("Config", &ServerConfig{Counters: map[string]int{"hits": 42}}), `config.counters["hits"]`), 42)
}

func TestMap_IntValues_Write(t *testing.T) {
	cfg := &ServerConfig{Counters: map[string]int{}}
	run(t, newSO("Config", cfg), `config.counters["reqs"] = 100`)
	if cfg.Counters["reqs"] != 100 {
		t.Errorf("expected 100, got %d", cfg.Counters["reqs"])
	}
}

func TestMap_Nil_NotNull(t *testing.T) {
	mustBool(t, run(t, newSO("Config", &ServerConfig{NilMap: nil}), `config.nilMap !== null`), true)
}

func TestMap_Nil_ObjectKeysEmpty(t *testing.T) {
	mustInt(t, run(t, newSO("Config", &ServerConfig{NilMap: nil}), `Object.keys(config.nilMap).length`), 0)
}

func TestMap_Nil_HasFalse(t *testing.T) {
	mustBool(t, run(t, newSO("Config", &ServerConfig{NilMap: nil}), `"key" in config.nilMap`), false)
}

// ==================== NIL ====================

func TestNil_PtrStruct_IsNull(t *testing.T) {
	mustBool(t, run(t, newSO("Config", &ServerConfig{NilPtr: nil}), `config.nilPtr === null`), true)
}

func TestNil_Slice_IsEmptyProxy(t *testing.T) {
	so := newSO("Config", &ServerConfig{NilSlice: nil})
	mustBool(t, run(t, so, `config.nilSlice !== null`), true)
	mustInt(t, run(t, so, `config.nilSlice.length`), 0)
}

func TestNil_Map_IsEmptyProxy(t *testing.T) {
	so := newSO("Config", &ServerConfig{NilMap: nil})
	mustBool(t, run(t, so, `config.nilMap !== null`), true)
	mustInt(t, run(t, so, `Object.keys(config.nilMap).length`), 0)
}

// ==================== COHÉRENCE LIVE ====================

func TestLive_GoMutation_VisibleFromNewVM(t *testing.T) {
	cfg := &ServerConfig{Host: "initial"}
	so := newSO("Config", cfg)
	mustStr(t, run(t, so, `config.host`), "initial")
	cfg.Host = "mutated"
	mustStr(t, run(t, so, `config.host`), "mutated")
}

func TestLive_JSMutation_VisibleFromGo(t *testing.T) {
	cfg := &ServerConfig{Port: 80}
	run(t, newSO("Config", cfg), `config.port = 8443`)
	if cfg.Port != 8443 {
		t.Errorf("expected 8443, got %d", cfg.Port)
	}
}

func TestLive_JSMutation_VisibleFromAnotherVM(t *testing.T) {
	cfg := &ServerConfig{Host: "A"}
	so := newSO("Config", cfg)
	vm1 := newVM(t, so)
	runInVM(t, vm1, `config.host = "B"`)
	vm2 := newVM(t, so)
	mustStr(t, runInVM(t, vm2, `config.host`), "B")
}

func TestLive_Concurrent_NoRace(t *testing.T) {
	so := newSO("Config", &ServerConfig{Port: 0})
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			vm := NewEmpty()
			vm.AttachGlobals()
			if err := so.attach(vm.Runtime); err != nil {
				return
			}
			_, _ = vm.RunString(`config.port`)
		}()
	}
	wg.Wait()
}

// ==================== SUBSCRIBE (Go API) ====================

func TestSubscribe_StructField(t *testing.T) {
	cfg := &ServerConfig{Host: "old"}
	so := newSO("Config", cfg)
	var events []ChangeEvent
	id := so.Subscribe(func(ev ChangeEvent) { events = append(events, ev) })
	defer so.Unsubscribe(id)

	run(t, so, `config.host = "new"`)

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Path != "host" {
		t.Errorf("path: want 'host', got %q", events[0].Path)
	}
	if events[0].OldValue != "old" {
		t.Errorf("oldValue: want 'old', got %v", events[0].OldValue)
	}
	if events[0].NewValue != "new" {
		t.Errorf("newValue: want 'new', got %v", events[0].NewValue)
	}
}

func TestSubscribe_NestedField_Path(t *testing.T) {
	so := newSO("Config", &ServerConfig{})
	var paths []string
	id := so.Subscribe(func(ev ChangeEvent) { paths = append(paths, ev.Path) })
	defer so.Unsubscribe(id)

	run(t, so, `config.tls.certFile = "/cert.pem"`)
	if len(paths) != 1 || paths[0] != "tls.certFile" {
		t.Errorf("expected ['tls.certFile'], got %v", paths)
	}
}

func TestSubscribe_SliceElement(t *testing.T) {
	cfg := &ServerConfig{Tags: []string{"a", "b"}}
	so := newSO("Config", cfg)
	var events []ChangeEvent
	id := so.Subscribe(func(ev ChangeEvent) { events = append(events, ev) })
	defer so.Unsubscribe(id)

	run(t, so, `config.tags[1] = "z"`)

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Path != "tags.1" {
		t.Errorf("path: want 'tags.1', got %q", events[0].Path)
	}
	if events[0].OldValue != "b" {
		t.Errorf("oldValue: want 'b', got %v", events[0].OldValue)
	}
}

func TestSubscribe_MapSetNewKey(t *testing.T) {
	cfg := &ServerConfig{Env: map[string]string{}}
	so := newSO("Config", cfg)
	var events []ChangeEvent
	id := so.Subscribe(func(ev ChangeEvent) { events = append(events, ev) })
	defer so.Unsubscribe(id)

	run(t, so, `config.env["FOO"] = "bar"`)

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Path != "env.FOO" {
		t.Errorf("path: want 'env.FOO', got %q", events[0].Path)
	}
	if events[0].OldValue != nil {
		t.Errorf("oldValue: want nil, got %v", events[0].OldValue)
	}
	if events[0].NewValue != "bar" {
		t.Errorf("newValue: want 'bar', got %v", events[0].NewValue)
	}
}

func TestSubscribe_MapDelete(t *testing.T) {
	cfg := &ServerConfig{Env: map[string]string{"K": "v"}}
	so := newSO("Config", cfg)
	var events []ChangeEvent
	id := so.Subscribe(func(ev ChangeEvent) { events = append(events, ev) })
	defer so.Unsubscribe(id)

	run(t, so, `delete config.env["K"]`)

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Path != "env.K" {
		t.Errorf("path: want 'env.K', got %q", events[0].Path)
	}
	if events[0].NewValue != nil {
		t.Errorf("newValue: want nil (deleted), got %v", events[0].NewValue)
	}
}

func TestSubscribe_NoEventOnRead(t *testing.T) {
	so := newSO("Config", &ServerConfig{Host: "x"})
	var count int32
	id := so.Subscribe(func(ev ChangeEvent) { atomic.AddInt32(&count, 1) })
	defer so.Unsubscribe(id)

	run(t, so, `config.host`)
	run(t, so, `config.port`)
	run(t, so, `config.tags`)

	if count != 0 {
		t.Errorf("expected 0 events on read, got %d", count)
	}
}

func TestSubscribe_MultipleSubscribers(t *testing.T) {
	so := newSO("Config", &ServerConfig{})
	var c1, c2 int32
	id1 := so.Subscribe(func(ev ChangeEvent) { atomic.AddInt32(&c1, 1) })
	id2 := so.Subscribe(func(ev ChangeEvent) { atomic.AddInt32(&c2, 1) })
	defer so.Unsubscribe(id1)
	defer so.Unsubscribe(id2)

	run(t, so, `config.host = "x"`)
	if c1 != 1 || c2 != 1 {
		t.Errorf("both should get 1 event: c1=%d c2=%d", c1, c2)
	}
}

func TestUnsubscribe_StopsEvents(t *testing.T) {
	so := newSO("Config", &ServerConfig{})
	var count int32
	id := so.Subscribe(func(ev ChangeEvent) { atomic.AddInt32(&count, 1) })
	run(t, so, `config.host = "a"`)
	so.Unsubscribe(id)
	run(t, so, `config.host = "b"`)
	if count != 1 {
		t.Errorf("expected 1 event before unsubscribe, got %d", count)
	}
}

func TestSubscribe_IDs_Unique(t *testing.T) {
	so := newSO("Config", &ServerConfig{})
	id1 := so.Subscribe(func(ChangeEvent) {})
	id2 := so.Subscribe(func(ChangeEvent) {})
	so.Unsubscribe(id1)
	so.Unsubscribe(id2)
	if id1 == id2 {
		t.Error("subscribe IDs must be unique")
	}
}

// ==================== SUBSCRIBE (JS API) ====================

func TestJS_Subscribe_IsFunction(t *testing.T) {
	so := newSO("Config", &ServerConfig{})
	mustBool(t, run(t, so, `typeof config.subscribe === "function"`), true)
	mustBool(t, run(t, so, `typeof config.unsubscribe === "function"`), true)
}

func TestJS_Subscribe_ReceivesEvent(t *testing.T) {
	so := newSO("Config", &ServerConfig{Port: 80})
	v := run(t, so, `
		var got = "";
		config.subscribe(function(ev) { got = ev.path + ":" + ev.newValue; });
		config.port = 9090;
		got;
	`)
	mustStr(t, v, "port:9090")
}

func TestJS_Unsubscribe_StopsEvents(t *testing.T) {
	so := newSO("Config", &ServerConfig{Port: 80})
	v := run(t, so, `
		var n = 0;
		var id = config.subscribe(function() { n++; });
		config.port = 9090;
		config.unsubscribe(id);
		config.port = 1234;
		n;
	`)
	mustInt(t, v, 1)
}

func TestJS_Subscribe_OldValue(t *testing.T) {
	so := newSO("Config", &ServerConfig{Host: "before"})
	v := run(t, so, `
		var old = "";
		config.subscribe(function(ev) { old = ev.oldValue; });
		config.host = "after";
		old;
	`)
	mustStr(t, v, "before")
}

func TestJS_Subscribe_NewValue(t *testing.T) {
	so := newSO("Config", &ServerConfig{})
	v := run(t, so, `
		var nv = "";
		config.subscribe(function(ev) { nv = ev.newValue; });
		config.host = "hello";
		nv;
	`)
	mustStr(t, v, "hello")
}

func TestJS_Subscribe_NestedPath(t *testing.T) {
	so := newSO("Config", &ServerConfig{})
	v := run(t, so, `
		var p = "";
		config.subscribe(function(ev) { p = ev.path; });
		config.tls.enabled = true;
		p;
	`)
	mustStr(t, v, "tls.enabled")
}

func TestJS_Subscribe_MultipleCallbacks(t *testing.T) {
	so := newSO("Config", &ServerConfig{})
	v := run(t, so, `
		var a = 0, b = 0;
		config.subscribe(function() { a++; });
		config.subscribe(function() { b++; });
		config.host = "x";
		a + "," + b;
	`)
	mustStr(t, v, "1,1")
}

func TestJS_Subscribe_SlicePath(t *testing.T) {
	so := newSO("Config", &ServerConfig{Tags: []string{"a", "b"}})
	v := run(t, so, `
		var p = "";
		config.subscribe(function(ev) { p = ev.path; });
		config.tags[0] = "z";
		p;
	`)
	mustStr(t, v, "tags.0")
}

func TestJS_Subscribe_MapPath(t *testing.T) {
	so := newSO("Config", &ServerConfig{Env: map[string]string{}})
	v := run(t, so, `
		var p = "";
		config.subscribe(function(ev) { p = ev.path; });
		config.env["MY_KEY"] = "val";
		p;
	`)
	mustStr(t, v, "env.MY_KEY")
}

// ==================== COERCE ====================

func TestCoerce_Int64ToInt(t *testing.T) {
	cfg := &ServerConfig{}
	run(t, newSO("Config", cfg), `config.port = 3000`)
	if cfg.Port != 3000 {
		t.Errorf("expected 3000, got %d", cfg.Port)
	}
}

func TestCoerce_Float64ToFloat32(t *testing.T) {
	cfg := &ServerConfig{}
	run(t, newSO("Config", cfg), `config.ratio = 0.5`)
	if cfg.Ratio < 0.49 || cfg.Ratio > 0.51 {
		t.Errorf("expected ~0.5, got %f", cfg.Ratio)
	}
}

func TestCoerce_Int64ToUint8(t *testing.T) {
	cfg := &ServerConfig{}
	run(t, newSO("Config", cfg), `config.byte = 200`)
	if cfg.Byte != 200 {
		t.Errorf("expected 200, got %d", cfg.Byte)
	}
}

func TestCoerce_NullToZeroInt(t *testing.T) {
	cfg := &ServerConfig{Port: 80}
	run(t, newSO("Config", cfg), `config.port = null`)
	if cfg.Port != 0 {
		t.Errorf("null → 0, got %d", cfg.Port)
	}
}

func TestCoerce_NullToEmptyString(t *testing.T) {
	cfg := &ServerConfig{Host: "x"}
	run(t, newSO("Config", cfg), `config.host = null`)
	if cfg.Host != "" {
		t.Errorf("null → \"\", got %q", cfg.Host)
	}
}

// ==================== structFieldByName (unit) ====================

func TestStructFieldByName_PascalCase(t *testing.T) {
	type S struct{ FooBar string }
	fv, _, ok := structFieldByName(reflect.ValueOf(S{FooBar: "x"}), "fooBar")
	if !ok || fv.String() != "x" {
		t.Errorf("PascalCase lookup failed: ok=%v", ok)
	}
}

func TestStructFieldByName_JsonTag(t *testing.T) {
	type S struct {
		CertPath string `json:"cert_path"`
	}
	fv, _, ok := structFieldByName(reflect.ValueOf(S{CertPath: "y"}), "cert_path")
	if !ok || fv.String() != "y" {
		t.Errorf("json tag lookup failed: ok=%v", ok)
	}
}

func TestStructFieldByName_CaseInsensitive(t *testing.T) {
	type S struct{ Host string }
	fv, _, ok := structFieldByName(reflect.ValueOf(S{Host: "z"}), "HOST")
	if !ok || fv.String() != "z" {
		t.Errorf("case-insensitive lookup failed: ok=%v", ok)
	}
}

func TestStructFieldByName_Unexported(t *testing.T) {
	type S struct {
		Exported   string
		unexported string //nolint
	}
	_, _, ok := structFieldByName(reflect.ValueOf(S{}), "unexported")
	if ok {
		t.Error("unexported field must not be accessible")
	}
}

func TestStructFieldByName_NotFound(t *testing.T) {
	type S struct{ Name string }
	_, _, ok := structFieldByName(reflect.ValueOf(S{}), "ghost")
	if ok {
		t.Error("non-existent field must return ok=false")
	}
}

// ==================== RegisterGlobal / AttachGlobals ====================

func TestRegisterGlobal_AttachGlobals(t *testing.T) {
	sharedObjectsMu.Lock()
	prev := sharedObjects
	sharedObjects = make(map[string]*SharedObject)
	sharedObjectsMu.Unlock()
	defer func() {
		sharedObjectsMu.Lock()
		sharedObjects = prev
		sharedObjectsMu.Unlock()
	}()

	type Cfg struct{ Value int }
	RegisterGlobal("TestCfg", &Cfg{Value: 42})

	vm := NewEmpty()
	vm.AttachGlobals()
	v, err := vm.RunString(`testCfg.value`)
	if err != nil {
		t.Fatalf("RunString: %v", err)
	}
	if v.ToInteger() != 42 {
		t.Errorf("expected 42, got %d", v.ToInteger())
	}
}

func TestRegisterGlobal_NameConversion(t *testing.T) {
	sharedObjectsMu.Lock()
	prev := sharedObjects
	sharedObjects = make(map[string]*SharedObject)
	sharedObjectsMu.Unlock()
	defer func() {
		sharedObjectsMu.Lock()
		sharedObjects = prev
		sharedObjectsMu.Unlock()
	}()

	type S struct{ X int }
	RegisterGlobal("AppConfig", &S{X: 7})
	vm := NewEmpty()
	vm.AttachGlobals()
	v, err := vm.RunString(`appConfig.x`)
	if err != nil {
		t.Fatalf("RunString: %v", err)
	}
	if v.ToInteger() != 7 {
		t.Errorf("expected 7, got %d", v.ToInteger())
	}
}

// ==================== joinPath / caseHelpers ====================

func TestJoinPath(t *testing.T) {
	cases := []struct{ parent, child, want string }{
		{"", "host", "host"},
		{"config", "host", "config.host"},
		{"tls", "certFile", "tls.certFile"},
		{"tags", "0", "tags.0"},
		{"env", "MY_KEY", "env.MY_KEY"},
	}
	for _, tc := range cases {
		if got := joinPath(tc.parent, tc.child); got != tc.want {
			t.Errorf("joinPath(%q,%q) = %q, want %q", tc.parent, tc.child, got, tc.want)
		}
	}
}

func TestPascalToCamel(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Host", "host"}, {"CertFile", "certFile"}, {"TLS", "tLS"}, {"", ""},
	}
	for _, tc := range cases {
		if got := pascalToCamel(tc.in); got != tc.want {
			t.Errorf("pascalToCamel(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCamelToPascal(t *testing.T) {
	cases := []struct{ in, want string }{
		{"host", "Host"}, {"certFile", "CertFile"}, {"", ""},
	}
	for _, tc := range cases {
		if got := camelToPascal(tc.in); got != tc.want {
			t.Errorf("camelToPascal(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// ==================== HasGlobal / UnregisterGlobal / Register ====================

func TestHasGlobal_True(t *testing.T) {
	sharedObjectsMu.Lock()
	prev := sharedObjects
	sharedObjects = make(map[string]*SharedObject)
	sharedObjectsMu.Unlock()
	defer func() {
		sharedObjectsMu.Lock()
		sharedObjects = prev
		sharedObjectsMu.Unlock()
	}()

	type S struct{ V int }
	RegisterGlobal("Foo", &S{V: 1})
	if !HasGlobal("Foo") {
		t.Error("HasGlobal should return true after RegisterGlobal")
	}
}

func TestHasGlobal_False(t *testing.T) {
	sharedObjectsMu.Lock()
	prev := sharedObjects
	sharedObjects = make(map[string]*SharedObject)
	sharedObjectsMu.Unlock()
	defer func() {
		sharedObjectsMu.Lock()
		sharedObjects = prev
		sharedObjectsMu.Unlock()
	}()

	if HasGlobal("DoesNotExist") {
		t.Error("HasGlobal should return false for unregistered name")
	}
}

func TestUnregisterGlobal_RemovesEntry(t *testing.T) {
	sharedObjectsMu.Lock()
	prev := sharedObjects
	sharedObjects = make(map[string]*SharedObject)
	sharedObjectsMu.Unlock()
	defer func() {
		sharedObjectsMu.Lock()
		sharedObjects = prev
		sharedObjectsMu.Unlock()
	}()

	type S struct{ V int }
	RegisterGlobal("Tmp", &S{V: 42})
	if !HasGlobal("Tmp") {
		t.Fatal("precondition: Tmp should be registered")
	}

	UnregisterGlobal("Tmp")
	if HasGlobal("Tmp") {
		t.Error("HasGlobal should return false after UnregisterGlobal")
	}
}

func TestUnregisterGlobal_NotAttachedToNewVM(t *testing.T) {
	sharedObjectsMu.Lock()
	prev := sharedObjects
	sharedObjects = make(map[string]*SharedObject)
	sharedObjectsMu.Unlock()
	defer func() {
		sharedObjectsMu.Lock()
		sharedObjects = prev
		sharedObjectsMu.Unlock()
	}()

	type S struct{ V int }
	RegisterGlobal("Gone", &S{V: 99})
	UnregisterGlobal("Gone")

	vm := NewEmpty()
	vm.AttachGlobals()

	v, err := vm.RunString(`typeof gone`)
	if err != nil {
		t.Fatalf("RunString: %v", err)
	}
	if v.String() != "undefined" {
		t.Errorf("unregistered global should be undefined in JS, got %q", v.String())
	}
}

func TestRegisterGlobal_IgnoreIfExist_True(t *testing.T) {
	sharedObjectsMu.Lock()
	prev := sharedObjects
	sharedObjects = make(map[string]*SharedObject)
	sharedObjectsMu.Unlock()
	defer func() {
		sharedObjectsMu.Lock()
		sharedObjects = prev
		sharedObjectsMu.Unlock()
	}()

	type S struct{ V int }
	first := &S{V: 1}
	second := &S{V: 2}

	RegisterGlobal("Once", first)
	RegisterGlobal("Once", second, true) // doit être ignoré

	vm := NewEmpty()
	vm.AttachGlobals()

	v, err := vm.RunString(`once.v`)
	if err != nil {
		t.Fatalf("RunString: %v", err)
	}
	if v.ToInteger() != 1 {
		t.Errorf("ignoreIfExist=true: expected first value (1), got %d", v.ToInteger())
	}
}

func TestRegisterGlobal_IgnoreIfExist_False(t *testing.T) {
	sharedObjectsMu.Lock()
	prev := sharedObjects
	sharedObjects = make(map[string]*SharedObject)
	sharedObjectsMu.Unlock()
	defer func() {
		sharedObjectsMu.Lock()
		sharedObjects = prev
		sharedObjectsMu.Unlock()
	}()

	type S struct{ V int }
	first := &S{V: 1}
	second := &S{V: 2}

	RegisterGlobal("Override", first)
	RegisterGlobal("Override", second) // remplace (pas d'ignoreIfExist)

	vm := NewEmpty()
	vm.AttachGlobals()

	v, err := vm.RunString(`override.v`)
	if err != nil {
		t.Fatalf("RunString: %v", err)
	}
	if v.ToInteger() != 2 {
		t.Errorf("without ignoreIfExist: expected second value (2), got %d", v.ToInteger())
	}
}

func TestRegisterGlobal_IgnoreIfExist_DefaultFalse(t *testing.T) {
	// Sans argument ignoreIfExist → comportement identique à false (écrase)
	sharedObjectsMu.Lock()
	prev := sharedObjects
	sharedObjects = make(map[string]*SharedObject)
	sharedObjectsMu.Unlock()
	defer func() {
		sharedObjectsMu.Lock()
		sharedObjects = prev
		sharedObjectsMu.Unlock()
	}()

	type S struct{ V int }
	RegisterGlobal("X", &S{V: 10})
	RegisterGlobal("X", &S{V: 20}) // écrase

	vm := NewEmpty()
	vm.AttachGlobals()

	v, err := vm.RunString(`x.v`)
	if err != nil {
		t.Fatalf("RunString: %v", err)
	}
	if v.ToInteger() != 20 {
		t.Errorf("expected 20, got %d", v.ToInteger())
	}
}

func TestRegister_SingleVM(t *testing.T) {
	// Register attache à une seule VM sans modifier le registre global
	sharedObjectsMu.Lock()
	prev := sharedObjects
	sharedObjects = make(map[string]*SharedObject)
	sharedObjectsMu.Unlock()
	defer func() {
		sharedObjectsMu.Lock()
		sharedObjects = prev
		sharedObjectsMu.Unlock()
	}()

	type S struct{ Name string }
	cfg := &S{Name: "local"}

	vm1 := NewEmpty()
	vm1.Register("LocalCfg", cfg)

	// vm1 doit avoir localCfg
	v, err := vm1.RunString(`localCfg.name`)
	if err != nil {
		t.Fatalf("vm1 RunString: %v", err)
	}
	mustStr(t, v, "local")

	// vm2 ne doit PAS avoir localCfg (non global)
	vm2 := NewEmpty()
	vm2.AttachGlobals() // installe les globaux, mais pas localCfg
	v2, err := vm2.RunString(`typeof localCfg`)
	if err != nil {
		t.Fatalf("vm2 RunString: %v", err)
	}
	if v2.String() != "undefined" {
		t.Errorf("Register should be VM-local, not global: got %q on vm2", v2.String())
	}

	// HasGlobal ne doit pas voir localCfg
	if HasGlobal("LocalCfg") {
		t.Error("Register should not affect the global registry")
	}
}

func TestRegister_Write_UpdatesGo(t *testing.T) {
	type S struct{ Port int }
	cfg := &S{Port: 80}

	vm := NewEmpty()
	vm.Register("Srv", cfg)

	_, err := vm.RunString(`srv.port = 9090`)
	if err != nil {
		t.Fatalf("RunString: %v", err)
	}
	if cfg.Port != 9090 {
		t.Errorf("Register write: expected 9090, got %d", cfg.Port)
	}
}

func TestRegister_Subscribe_Works(t *testing.T) {
	type S struct{ V int }
	cfg := &S{V: 0}

	vm := NewEmpty()
	vm.AttachGlobals()
	so := &SharedObject{name: "Test", data: cfg}
	_ = so.attach(vm.Runtime)

	var events []ChangeEvent
	so.Subscribe(func(ev ChangeEvent) { events = append(events, ev) })

	_, err := vm.RunString(`test.v = 42`)
	if err != nil {
		t.Fatalf("RunString: %v", err)
	}
	if len(events) != 1 || events[0].NewValue != 42 {
		t.Errorf("subscribe via Register: expected 1 event with value 42, got %v", events)
	}
}
