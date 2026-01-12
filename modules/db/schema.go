package db

import (
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
}

// Model - Model Mongoose-like
type Model struct {
	Name   string
	Schema *Schema
	db     *gorm.DB
}

// Document - Document Mongoose-like
type Document struct {
	Data  map[string]interface{}
	Model *Model
	ID    string
	isNew bool
}

// Connection - Connexion Mongoose-like
type Connection struct {
	db      *gorm.DB
	vm      *goja.Runtime
	models  map[string]*Model
	schemas map[string]*Schema
	mutex   sync.RWMutex
}
