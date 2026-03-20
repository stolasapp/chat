package hub

import (
	"sync"

	"github.com/stolasapp/chat/internal/match"
)

// ClientRegistry manages connected client state. Detached clients
// remain in the registry; their timers live on the Client itself.
type ClientRegistry interface {
	// Add registers a client, returning the new count.
	Add(client *Client) int
	// Remove removes a client. Returns whether it was found and
	// the new count.
	Remove(client *Client) (found bool, count int)
	// ByToken looks up a client by token.
	ByToken(token match.Token) *Client
	// Len returns the total client count.
	Len() int
	// Snapshot returns all clients and the count atomically.
	Snapshot() ([]*Client, int)
	// Clear removes all entries.
	Clear()
}

// Registry is the default ClientRegistry backed by an in-memory
// map.
type Registry struct {
	tokens map[match.Token]*Client // +checklocks:mu
	mu     sync.RWMutex
}

var _ ClientRegistry = (*Registry)(nil)

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		tokens: make(map[match.Token]*Client),
	}
}

// Add registers a client, returning the new count.
func (r *Registry) Add(client *Client) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tokens[client.Token()] = client
	return len(r.tokens)
}

// Remove removes a client. Returns whether it was found and the
// new count.
func (r *Registry) Remove(client *Client) (bool, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	existing, ok := r.tokens[client.Token()]
	if !ok || existing != client {
		return false, len(r.tokens)
	}
	delete(r.tokens, client.Token())
	return true, len(r.tokens)
}

// ByToken looks up a client by token.
func (r *Registry) ByToken(token match.Token) *Client {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.tokens[token]
}

// Len returns the total client count.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.tokens)
}

// Snapshot returns all clients and the count atomically.
func (r *Registry) Snapshot() ([]*Client, int) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	clients := make([]*Client, 0, len(r.tokens))
	for _, client := range r.tokens {
		clients = append(clients, client)
	}
	return clients, len(r.tokens)
}

// Clear removes all entries.
func (r *Registry) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	clear(r.tokens)
}
