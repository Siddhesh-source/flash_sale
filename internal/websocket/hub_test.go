package websocket_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	ws "github.com/flashsale/backend/internal/websocket"
	gorillaws "github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

// mockConn wraps a pair of net.Conn ends (provided by httptest) to hand to NewClient.
// Instead we create real gorilla websocket connections via httptest for realism.

func newTestServerAndHub(t *testing.T) (*ws.Hub, *httptest.Server) {
	t.Helper()
	hub := ws.NewHub()
	go hub.Run()

	upgrader := gorillaws.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		saleID := r.URL.Query().Get("sale")
		userID := r.URL.Query().Get("user")
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		client := ws.NewClient(hub, conn)
		hub.Subscribe(saleID, userID, client)
		go client.StartWritePump()
		go client.StartReadPump()
	}))

	return hub, srv
}

func dialWS(t *testing.T, srv *httptest.Server, saleID, userID string) *gorillaws.Conn {
	t.Helper()
	url := "ws" + srv.URL[4:] + "?sale=" + saleID + "&user=" + userID
	conn, _, err := gorillaws.DefaultDialer.Dial(url, nil)
	require.NoError(t, err)
	return conn
}

func TestBroadcastToRoom(t *testing.T) {
	defer goleak.VerifyNone(t,
		goleak.IgnoreAnyFunction("github.com/flashsale/backend/internal/websocket.(*Client).StartWritePump"),
		goleak.IgnoreAnyFunction("github.com/flashsale/backend/internal/websocket.(*Client).StartReadPump"),
		goleak.IgnoreTopFunction("(*Hub).Run"),
	)

	hub, srv := newTestServerAndHub(t)
	defer srv.Close()

	const numClients = 100
	conns := make([]*gorillaws.Conn, numClients)
	for i := 0; i < numClients; i++ {
		conns[i] = dialWS(t, srv, "abc", "")
		defer conns[i].Close()
	}

	// Small settle time for all goroutines to register
	time.Sleep(50 * time.Millisecond)

	payload := []byte(`{"event":"stock_update","data":{"remaining":43}}`)
	hub.Publish("abc", payload)

	received := make(chan struct{}, numClients)
	for i := 0; i < numClients; i++ {
		go func(c *gorillaws.Conn) {
			c.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
			_, msg, err := c.ReadMessage()
			if err == nil && len(msg) > 0 {
				received <- struct{}{}
			}
		}(conns[i])
	}

	deadline := time.After(300 * time.Millisecond)
	count := 0
	for count < numClients {
		select {
		case <-received:
			count++
		case <-deadline:
			t.Fatalf("only %d/%d clients received within deadline", count, numClients)
		}
	}
	assert.Equal(t, numClients, count)
}

func TestUserTargetedEvent(t *testing.T) {
	defer goleak.VerifyNone(t,
		goleak.IgnoreAnyFunction("github.com/flashsale/backend/internal/websocket.(*Client).StartWritePump"),
		goleak.IgnoreAnyFunction("github.com/flashsale/backend/internal/websocket.(*Client).StartReadPump"),
		goleak.IgnoreTopFunction("(*Hub).Run"),
	)

	hub, srv := newTestServerAndHub(t)
	defer srv.Close()

	user1 := dialWS(t, srv, "abc", "user-1")
	defer user1.Close()
	user2 := dialWS(t, srv, "abc", "user-2")
	defer user2.Close()
	user3 := dialWS(t, srv, "abc", "")
	defer user3.Close()

	time.Sleep(50 * time.Millisecond)

	payload := []byte(`{"event":"waitlist_promoted","data":{"your_reservation_id":"res-xyz"}}`)
	hub.PublishToUser("user-1", payload)

	// user-1 must receive
	user1.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	_, msg, err := user1.ReadMessage()
	require.NoError(t, err)
	assert.Contains(t, string(msg), "waitlist_promoted")

	// user-2 must NOT receive (read times out)
	user2.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	_, _, err = user2.ReadMessage()
	assert.Error(t, err, "user-2 should not receive user-1 targeted event")

	// user-3 (no userID) must NOT receive
	user3.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	_, _, err = user3.ReadMessage()
	assert.Error(t, err, "anonymous client should not receive user-1 targeted event")
}
