package hub

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/stolasapp/chat/internal/match"
)

func testClient(token match.Token) *Client {
	ctx, cancel := context.WithCancelCause(context.Background())
	return &Client{
		token:  token,
		ctx:    ctx,
		cancel: cancel,
		send:   make(chan []byte, 1),
		pumpWG: &sync.WaitGroup{},
	}
}

func TestRegistry_AddAndLen(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()

	c := testClient("tok-1")
	count := reg.Add(c)
	assert.Equal(t, 1, count)
	assert.Equal(t, 1, reg.Len())
}

func TestRegistry_Remove(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()

	c := testClient("tok-1")
	reg.Add(c)

	found, count := reg.Remove(c)
	assert.True(t, found)
	assert.Equal(t, 0, count)
	assert.Nil(t, reg.ByToken("tok-1"))
}

func TestRegistry_RemoveWrongClient(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()

	clientOne := testClient("tok-1")
	clientTwo := testClient("tok-1")
	reg.Add(clientOne)

	// clientTwo has the same token but is not the registered client
	found, _ := reg.Remove(clientTwo)
	assert.False(t, found)
	assert.Equal(t, clientOne, reg.ByToken("tok-1"))
}

func TestRegistry_RemoveNotFound(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()

	client := testClient("tok-1")
	found, _ := reg.Remove(client)
	assert.False(t, found)
}

func TestRegistry_ByToken(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()

	client := testClient("tok-1")
	reg.Add(client)

	assert.Equal(t, client, reg.ByToken("tok-1"))
	assert.Nil(t, reg.ByToken("tok-2"))
}

func TestRegistry_Snapshot(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()

	clientOne := testClient("tok-1")
	clientTwo := testClient("tok-2")
	reg.Add(clientOne)
	reg.Add(clientTwo)

	snap, count := reg.Snapshot()
	assert.Len(t, snap, 2)
	assert.Equal(t, 2, count)
}

func TestRegistry_Clear(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()

	clientOne := testClient("tok-1")
	reg.Add(clientOne)

	clientTwo := testClient("tok-2")
	reg.Add(clientTwo)

	reg.Clear()

	assert.Equal(t, 0, reg.Len())
	assert.Nil(t, reg.ByToken("tok-1"))
	assert.Nil(t, reg.ByToken("tok-2"))
}
