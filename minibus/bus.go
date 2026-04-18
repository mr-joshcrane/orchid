package minibus

import "sync"

// Bus represents a simple publish-subscribe mechanism.
type Bus struct {
	subs    map[string]map[int]func(string)
	nextID  int
	mu      sync.RWMutex
}

// New creates and returns a new Bus instance.
func New() *Bus {
	return &Bus{subs: make(map[string]map[int]func(string)), nextID: 1}
}

// Subscribe registers a function to be called when a specific topic is published.
// It returns a unique ID for the subscription, which can be used for unsubscribing.
func (b *Bus) Subscribe(topic string, fn func(string)) int {
	b.mu.Lock()
	defer b.mu.Unlock()

	if _, ok := b.subs[topic]; !ok {
		b.subs[topic] = make(map[int]func(string))
	}
	id := b.nextID
	b.subs[topic][id] = fn
	b.nextID++
	return id
}

// Unsubscribe removes a subscription by its unique ID across all topics.
// It returns true if the subscription was found and removed, false otherwise.
func (b *Bus) Unsubscribe(id int) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	for topic, subs := range b.subs {
		if _, ok := subs[id]; ok {
			delete(subs, id)
			if len(subs) == 0 {
				delete(b.subs, topic)
			}
			return true
		}
	}
	return false
}

// Publish sends a payload to all subscribers of the given topic.
func (b *Bus) Publish(topic string, payload string) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if subs, ok := b.subs[topic]; ok {
		// Iterate over a copy of functions if modification during iteration is possible,
		// but since we are only reading here, direct iteration is fine.
		for _, fn := range subs {
			fn(payload)
		}
	}
}

// Topics returns a slice of all currently subscribed topics.
func (b *Bus) Topics() []string {
	b.mu.RLock()
	defer b.mu.RUnlock()

	topics := make([]string, 0, len(b.subs))
	for topic := range b.subs {
		topics = append(topics, topic)
	}
	return topics
}

// Close cleans up the bus resources by clearing all subscriptions.
func (b *Bus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Clear all subscriptions
	b.subs = make(map[string]map[int]func(string))
	b.nextID = 0
}