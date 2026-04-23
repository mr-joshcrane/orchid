package main

import (
	"fmt"
	"os"

	"github.com/mr-joshcrane/orchid"
	"github.com/mr-joshcrane/orchid/packgen"
)

func main() {
	model := os.Getenv("BEDROCK_MODEL")
	if model == "" {
		fmt.Fprintln(os.Stderr, "set BEDROCK_MODEL to a Bedrock model ID or inference profile ARN")
		os.Exit(1)
	}

	gen, err := orchid.NewBedrockGenerator(model)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create generator: %v\n", err)
		os.Exit(1)
	}

	projects := []packgen.Project{
		{
			Name: "minidb",
			Gen:  gen,
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
			Name: "minicache",
			Gen:  gen,
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
	}

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
