package conversation

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/matrix-org/complement/internal/client"
)

// A Conversation is a synchronisation mechanism designed to co-ordinate multiple client.CSAPI instances
type Conversation struct {
	t               *testing.T
	mu              *sync.Mutex
	exchangeTimeout time.Duration
	clients         map[string]*client.CSAPI
}

func (c *Conversation) AddClient(client *client.CSAPI) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.clients[client.UserID] = client
}
func (c *Conversation) RemoveClient(client *client.CSAPI) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.clients, client.UserID)
}

func (c *Conversation) Do(speaker *client.CSAPI, ex Exchange, notUsers []*client.CSAPI) {
	c.mu.Lock()
	defer c.mu.Unlock()
	speakerExists := false
	notUserMap := make(map[string]bool)
	for _, nu := range notUsers {
		notUserMap[nu.UserID] = true
	}
	var listeners []*client.CSAPI
	var listenerUserIDs []string
	for userID, cli := range c.clients {
		if speaker.UserID == userID {
			speakerExists = true
			continue
		}
		if notUserMap[userID] {
			continue
		}
		listeners = append(listeners, cli)
		listenerUserIDs = append(listenerUserIDs, cli.UserID)
	}
	if !speakerExists {
		c.t.Fatalf("Conversation.Do: speaker %s is not part of this conversation.", speaker.UserID)
	}
	c.t.Logf("Conversation.Do: %s is starting a new exchange", speaker.UserID)
	ex.Do(c.t, speaker)
	var wg sync.WaitGroup
	wg.Add(len(listeners))
	for i := range listeners {
		go func(listener *client.CSAPI) {
			defer wg.Done()
			ex.Listen(c.t, listener)
		}(listeners[i])
	}
	c.t.Logf("Conversation.Do: waiting for %v to receive this exchange", listenerUserIDs)
	ch := make(chan struct{})
	go func() {
		wg.Wait()
		fmt.Println("closing ch")
		close(ch)
	}()
	select {
	case <-time.After(c.exchangeTimeout):
		c.t.Fatalf("Conversation timed out after %v", c.exchangeTimeout)
	case <-ch:
	}
	c.t.Logf("Conversation.Do: exchange completed.")
}

type Option func(c *Conversation)

// Create a new Conversation
func New(t *testing.T, opts ...Option) *Conversation {
	c := Conversation{
		t:               t,
		mu:              &sync.Mutex{},
		exchangeTimeout: 5 * time.Second,
		clients:         make(map[string]*client.CSAPI),
	}
	for _, opt := range opts {
		opt(&c)
	}
	return &c
}

func WithExchangeTimeout(timeout time.Duration) Option {
	return func(c *Conversation) {
		c.exchangeTimeout = timeout
	}
}

func WithUsers(clients ...*client.CSAPI) Option {
	return func(c *Conversation) {
		for _, cli := range clients {
			c.AddClient(cli)
		}
	}
}
