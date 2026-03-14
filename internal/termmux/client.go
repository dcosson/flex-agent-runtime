package termmux

import (
	"sync"
)

// Client represents a connected client that can receive PTY output from a session.
type Client struct {
	ID string

	mu     sync.Mutex
	output chan []byte // Buffered channel for output data
	closed bool
}

const defaultClientOutputBuffer = 4096

// NewClient creates a new client with a buffered output channel.
func NewClient(id string) *Client {
	return &Client{
		ID:     id,
		output: make(chan []byte, defaultClientOutputBuffer),
	}
}

// Output returns the read-only channel for receiving PTY output.
func (c *Client) Output() <-chan []byte {
	return c.output
}

// Send sends data to the client's output channel. Non-blocking:
// drops data if the client's buffer is full.
func (c *Client) Send(data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return
	}

	select {
	case c.output <- data:
	default:
		// Client is slow — drop rather than block
	}
}

// Close marks the client as closed and closes the output channel.
func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return
	}
	c.closed = true
	close(c.output)
}

// ClientManager handles multi-client attach/detach for a session.
type ClientManager struct {
	mu      sync.RWMutex
	clients map[string]*Client
}

// NewClientManager creates a new client manager.
func NewClientManager() *ClientManager {
	return &ClientManager{
		clients: make(map[string]*Client),
	}
}

// Attach adds a client and returns it. If a client with the same ID
// already exists, the old one is closed and replaced.
func (cm *ClientManager) Attach(id string) *Client {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if existing, ok := cm.clients[id]; ok {
		existing.Close()
	}

	client := NewClient(id)
	cm.clients[id] = client
	return client
}

// Detach removes and closes a client by ID.
func (cm *ClientManager) Detach(id string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if client, ok := cm.clients[id]; ok {
		client.Close()
		delete(cm.clients, id)
	}
}

// FanOut sends data to all attached clients. Non-blocking per client.
func (cm *ClientManager) FanOut(data []byte) {
	cm.mu.RLock()
	clients := make([]*Client, 0, len(cm.clients))
	for _, c := range cm.clients {
		clients = append(clients, c)
	}
	cm.mu.RUnlock()

	for _, c := range clients {
		c.Send(data)
	}
}

// Count returns the number of attached clients.
func (cm *ClientManager) Count() int {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return len(cm.clients)
}

// CloseAll closes and removes all clients.
func (cm *ClientManager) CloseAll() {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	for id, client := range cm.clients {
		client.Close()
		delete(cm.clients, id)
	}
}

// List returns the IDs of all attached clients.
func (cm *ClientManager) List() []string {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	ids := make([]string, 0, len(cm.clients))
	for id := range cm.clients {
		ids = append(ids, id)
	}
	return ids
}
