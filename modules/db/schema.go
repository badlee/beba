package db

import (
	"fmt"
	"http-server/plugins/js"
	"reflect"
	"strings"
	"sync"

	"github.com/dop251/goja"
	"gorm.io/gorm"
)

// Schema - Définition du schéma Mongoose-like
type Schema struct {
	Paths      map[string]SchemaType
	Methods    map[string]string
	Statics    map[string]string
	Virtuals   map[string]VirtualDef
	Middleware map[string][]MiddlewareDef
	vm         *goja.Runtime
}

type VirtualDef struct {
	Get string
	Set string
}

type MiddlewareDef struct {
	Type   string // 'pre' or 'post'
	Action string // 'save', 'remove', 'find', etc.
	Fn     string // code JS
}

type SchemaType struct {
	Type     string      `json:"type"`
	Required bool        `json:"required,omitempty"`
	Default  interface{} `json:"default,omitempty"`
	Index    bool        `json:"index,omitempty"`
	Unique   bool        `json:"unique,omitempty"`
	Validate string      `json:"validate,omitempty"` // Code JS de validation
	Ref      string      `json:"ref,omitempty"`      // Lien vers un autre schéma [Schema].[field]
	Has      string      `json:"has,omitempty"`      // 'one' or 'many'
	OnDelete string      `json:"on_delete,omitempty"`
	OnUpdate string      `json:"on_update,omitempty"`
}

type Item interface {
	GetName() string
	GetCode() string
	GetItemType() string
}

// Deferred items to add after conn.Model() creates the base schema
type SchemaItem struct {
	itemType string // "VIRTUAL", "PRE", "POST", "METHOD", "STATIC"
	name     string
	code     string
	args     string // for STATIC
}

// GetCode implements [db.Item].
func (d *SchemaItem) GetCode() string {
	getCode := strings.TrimSpace(d.code)
	if js.IsFunction(getCode) {
		return getCode
	}
	c, _ := js.EnsureReturnStrict(getCode)
	return fmt.Sprintf("function(%s) { with(this){ %s; } }", d.args, c)
}

// GetItemType implements [db.Item].
func (d *SchemaItem) GetItemType() string {
	return d.itemType
}

// GetName implements [db.Item].
func (d *SchemaItem) GetName() string {
	return d.name
}

func NewSchemaItem(itemType, name, code string, args ...string) Item {

	return &SchemaItem{
		itemType: itemType,
		name:     name,
		code:     code,
		args:     strings.Join(args, ", "),
	}
}

// Model - Model Mongoose-like
type Model struct {
	Name       string
	Schema     *Schema
	db         *gorm.DB
	conn       *Connection
	NativeType reflect.Type
	isBuilding bool
}

func (m *Model) AddMethod(item Item) {
	m.Schema.Methods[item.GetName()] = item.GetCode()
}

func (m *Model) AddVirtual(item Item) {
	m.Schema.Virtuals[item.GetName()] = VirtualDef{
		Get: item.GetCode(),
	}
}

func (m *Model) AddMiddleware(isPre bool, item Item) {
	t := "pre"
	if !isPre {
		t = "post"
	}
	m.Schema.Middleware[item.GetName()] = append(m.Schema.Middleware[item.GetName()], MiddlewareDef{
		Type:   t,
		Action: item.GetName(),
		Fn:     item.GetCode(),
	})

}

func (m *Model) AddStatic(item Item) {
	m.Schema.Statics[item.GetName()] = item.GetCode()

}

// Document - Document Mongoose-like
type Document struct {
	Data  map[string]interface{}
	Model *Model
	ID    string
	isNew bool
}

type ProxyBuilder func(vm *goja.Runtime, name string, data interface{}) goja.Value

// Connection - Connexion Mongoose-like
type Connection struct {
	sync.RWMutex
	db     *gorm.DB
	vm     *goja.Runtime
	models map[string]*Model
	mutex  sync.RWMutex
	name   string
}

func (conn *Connection) Close() (err error) {
	conn.RLock()
	d := conn.db
	conn.RUnlock()

	if d != nil {
		if sqlDB, e := d.DB(); e == nil {
			err = sqlDB.Close()
		}
	}

	AllConnectionsMu.Lock()
	defer AllConnectionsMu.Unlock()
	delete(AllConnections, conn.name)
	conn.cleanup()
	return
}

func (conn *Connection) cleanup() {
	conn.Lock()
	defer conn.Unlock()
	for k := range conn.models {
		delete(conn.models, k)
	}
	conn.name = ""
	conn.vm = nil
	conn.db = nil
}

func (conn *Connection) GetDB() *gorm.DB {
	conn.RLock()
	defer conn.RUnlock()
	return conn.db
}

func (conn *Connection) GetModels() map[string]*Model {
	conn.RLock()
	defer conn.RUnlock()
	return conn.models
}

func (conn *Connection) Name() string {
	conn.RLock()
	defer conn.RUnlock()
	return conn.name
}

func (conn *Connection) AddConnection(c *Connection) {
	AllConnectionsMu.Lock()
	defer AllConnectionsMu.Unlock()
	AllConnections[c.name] = c
}

func (conn *Connection) GetConnections() map[string]*Connection {
	AllConnectionsMu.RLock()
	defer AllConnectionsMu.RUnlock()
	return AllConnections
}

var AllConnections map[string]*Connection = make(map[string]*Connection)
var AllConnectionsMu sync.RWMutex

const DefaultConnName = "DEFAULT"

// NewGlobalConnection instantiates a Connection used by global injected properties without specific VMs.
func NewConnection(db *gorm.DB, name ...string) *Connection {
	// AutoMigrate initial tables
	var n string = DefaultConnName
	if len(name) > 0 {
		n = name[0]
	}
	n = strings.ToUpper(n)
	db.AutoMigrate(&SchemaRecord{})
	conn := &Connection{
		db:     db,
		models: make(map[string]*Model),
		name:   n,
	}
	AllConnectionsMu.Lock()
	defer AllConnectionsMu.Unlock()
	if oldConn, ok := AllConnections[n]; ok {
		// silent close old connection
		delete(AllConnections, n)
		oldConn.cleanup()
	}
	AllConnections[n] = conn
	return conn
}

func GetConnection(name ...string) *Connection {
	var n string = DefaultConnName
	if len(name) > 0 {
		n = name[0]
	}
	n = strings.ToUpper(n)
	AllConnectionsMu.RLock()
	defer AllConnectionsMu.RUnlock()
	return AllConnections[n]
}
func RegisterConnection(conn *Connection, name ...string) {
	var n string = conn.name
	if len(name) > 0 {
		n = name[0]
	}
	n = strings.ToUpper(n)

	conn.Lock()
	conn.name = n
	conn.Unlock()

	AllConnectionsMu.Lock()
	AllConnections[n] = conn
	AllConnectionsMu.Unlock()
}
func HasConnection(name ...string) bool {
	AllConnectionsMu.RLock()
	defer AllConnectionsMu.RUnlock()
	var n string = DefaultConnName
	if len(name) > 0 {
		n = name[0]
	}
	n = strings.ToUpper(n)
	_, ok := AllConnections[n]
	return ok
}
func GetDefaultConnection() *Connection {
	return GetConnection()
}

func RegisterDefaultConnection(conn *Connection) {
	RegisterConnection(conn, DefaultConnName)
}
func HasDefaultConnection() bool {
	return HasConnection()
}
func GetDefaultlDatabaseName() string {
	return DefaultConnName
}
