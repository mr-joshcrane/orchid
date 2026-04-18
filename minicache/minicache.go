package minicache

import "time"

// entry holds the stored value and its expiration time.
type entry struct {
	value     string
	expiresAt time.Time
}

// Cache implements a simple in-memory key-value store with TTL.
type Cache struct {
	entries map[string]entry
}

// New creates and returns a new Cache instance.
func New() *Cache {
	return &Cache{entries: make(map[string]entry)}
}

// Set adds or updates a key-value pair in the cache with a specified Time To Live (TTL).
func (c *Cache) Set(key string, value string, ttl time.Duration) {
	c.entries[key] = entry{
		value:     value,
		expiresAt: time.Now().Add(ttl),
	}
}

// Get retrieves a value from the cache. It returns the value and true if found and not expired,
// or an empty string and false otherwise. Expired entries are automatically deleted.
func (c *Cache) Get(key string) (string, bool) {
	entry, ok := c.entries[key]
	if !ok {
		return "", false
	}
	if time.Now().After(entry.expiresAt) {
		delete(c.entries, key)
		return "", false
	}
	return entry.value, true
}

// Delete removes a key from the cache if it exists.
func (c *Cache) Delete(key string) {
	if _, ok := c.entries[key]; ok {
		delete(c.entries, key)
	}
}

// Cleanup iterates over all entries and deletes any that have expired.
// It returns the count of deleted entries.
func (c *Cache) Cleanup() int {
	now := time.Now()
	deletedCount := 0
	for key, entry := range c.entries {
		if entry.expiresAt.Before(now) {
			delete(c.entries, key)
			deletedCount++
		}
	}
	return deletedCount
}

// Len returns the current number of items in the cache.
func (c *Cache) Len() int {
	return len(c.entries)
}

// Keys returns a slice containing all keys currently stored in the cache.
func (c *Cache) Keys() []string {
	keys := make([]string, 0, len(c.entries))
	for key := range c.entries {
		keys = append(keys, key)
	}
	return keys
}