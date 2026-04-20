package main

import (
	"fmt"
	"os"

	"github.com/mr-joshcrane/orchid/packgen"
)

const endpoint = "http://localhost:8080"
const model = "gemma-4-e4b-it-Q4_K_M.gguf"

var projects = []packgen.Project{
	{
		Name:     "minidb",
		Endpoint: endpoint,
		Model:    model,
		Plan: `type DB struct {
	tables map[string]*Table
}

type Table struct {
	rows    map[string]map[string]string
	indexes map[string]map[string][]string
}

func NewDB() *DB
func (db *DB) CreateTable(name string) error
func (db *DB) Table(name string) (*Table, error)
func (t *Table) Insert(id string, row map[string]string) error
func (t *Table) Get(id string) (map[string]string, bool)
func (t *Table) Delete(id string) error
func (t *Table) Query(field string, value string) []map[string]string
func (t *Table) AddIndex(field string)`,
	},
	{
		Name:     "minicache",
		Endpoint: endpoint,
		Model:    model,
		Plan: `type Cache struct {
	entries map[string]entry
}

type entry struct {
	value     string
	expiresAt time.Time
}

func New() *Cache
func (c *Cache) Set(key string, value string, ttl time.Duration)
func (c *Cache) Get(key string) (string, bool)
func (c *Cache) Delete(key string)
func (c *Cache) Cleanup() int
func (c *Cache) Len() int
func (c *Cache) Keys() []string`,
	},
	{
		Name:     "minibus",
		Endpoint: endpoint,
		Model:    model,
		Plan: `type Bus struct {
	subs   map[string]map[int]func(string)
	nextID int
}

func New() *Bus
func (b *Bus) Subscribe(topic string, fn func(string)) int
func (b *Bus) Unsubscribe(id int) bool
func (b *Bus) Publish(topic string, payload string)
func (b *Bus) Topics() []string
func (b *Bus) Close()`,
	},
	{
		Name:     "minirouter",
		Endpoint: endpoint,
		Model:    model,
		Plan: `type Router struct {
	routes []route
}

type route struct {
	method  string
	parts   []string
	handler func(http.ResponseWriter, *http.Request, Params)
}

type Params map[string]string

func New() *Router
func (r *Router) Handle(method string, pattern string, handler func(http.ResponseWriter, *http.Request, Params))
func (r *Router) Lookup(method string, path string) (func(http.ResponseWriter, *http.Request, Params), Params, bool)
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request)`,
	},
	{
		Name:     "minifsm",
		Endpoint: endpoint,
		Model:    model,
		Plan: `type FSM struct {
	initial     string
	current     string
	transitions map[string]map[string]transition
}

type transition struct {
	to     string
	action func() error
}

func New(initial string) *FSM
func (f *FSM) On(from string, event string, to string, action func() error)
func (f *FSM) Send(event string) error
func (f *FSM) State() string
func (f *FSM) Can(event string) bool
func (f *FSM) Reset()`,
	},
	{
		Name:     "minilog",
		Endpoint: endpoint,
		Model:    model,
		Plan: `type Level int

const (
	LevelInfo Level = iota
	LevelWarn
	LevelError
)

type Logger struct {
	w         io.Writer
	minLevel  Level
	fields    map[string]string
	formatter func(Level, string, map[string]string) string
}

func New(w io.Writer) *Logger
func (l *Logger) WithFields(fields map[string]string) *Logger
func (l *Logger) SetLevel(level Level)
func (l *Logger) SetFormatter(fn func(Level, string, map[string]string) string)
func (l *Logger) log(level Level, msg string, kvs []string)
func (l *Logger) Info(msg string, kvs ...string)
func (l *Logger) Warn(msg string, kvs ...string)
func (l *Logger) Error(msg string, kvs ...string)`,
	},
}

func main() {
	selected := projects
	if len(os.Args) > 1 {
		name := os.Args[1]
		for _, p := range projects {
			if p.Name == name {
				selected = []packgen.Project{p}
				break
			}
		}
	}

	cfg := packgen.DefaultConfig()
	for _, p := range selected {
		fmt.Printf("\n========== Building %s ==========\n\n", p.Name)
		if err := packgen.Build(p, cfg); err != nil {
			fmt.Printf("Failed: %s\n", err)
			continue
		}
		fmt.Printf("\n========== Done: %s ==========\n\n", p.Name)
	}
}
