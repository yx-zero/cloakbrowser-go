// Package cdp is a minimal, pure-Go Chrome DevTools Protocol client. It speaks
// raw JSON over a WebSocket to a Chromium browser started with
// --remote-debugging-port, replacing the Playwright/Node transport entirely.
//
// It supports flat sessions (Target.attachToTarget {flatten:true}): every
// command may carry a sessionId, and events are routed to per-session listeners.
package cdp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// Conn is a CDP connection over a single WebSocket.
type Conn struct {
	ws     *websocket.Conn
	nextID int64

	mu       sync.Mutex
	pending  map[int64]chan rawResponse
	closed   bool
	closeErr error

	listenersMu sync.RWMutex
	// listeners keyed by sessionId ("" = browser-level), then by method.
	listeners map[string]map[string][]func(json.RawMessage)

	done chan struct{}
}

type command struct {
	ID        int64  `json:"id"`
	Method    string `json:"method"`
	Params    any    `json:"params,omitempty"`
	SessionID string `json:"sessionId,omitempty"`
}

type rawResponse struct {
	result json.RawMessage
	err    *ProtocolError
}

type incoming struct {
	ID        int64           `json:"id"`
	Method    string          `json:"method"`
	Params    json.RawMessage `json:"params"`
	Result    json.RawMessage `json:"result"`
	Error     *ProtocolError  `json:"error"`
	SessionID string          `json:"sessionId"`
}

// ProtocolError is a CDP error returned by the browser.
type ProtocolError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    string `json:"data"`
}

func (e *ProtocolError) Error() string {
	if e.Data != "" {
		return fmt.Sprintf("cdp error %d: %s (%s)", e.Code, e.Message, e.Data)
	}
	return fmt.Sprintf("cdp error %d: %s", e.Code, e.Message)
}

// Dial connects to a DevTools WebSocket URL (webSocketDebuggerUrl).
func Dial(ctx context.Context, wsURL string) (*Conn, error) {
	ws, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{})
	if err != nil {
		return nil, fmt.Errorf("cdp dial: %w", err)
	}
	// Chromium can send large DOM payloads — lift the read limit.
	ws.SetReadLimit(256 * 1024 * 1024)

	c := &Conn{
		ws:        ws,
		pending:   make(map[int64]chan rawResponse),
		listeners: make(map[string]map[string][]func(json.RawMessage)),
		done:      make(chan struct{}),
	}
	go c.readLoop()
	return c, nil
}

func (c *Conn) readLoop() {
	defer close(c.done)
	for {
		_, data, err := c.ws.Read(context.Background())
		if err != nil {
			c.fail(err)
			return
		}
		var msg incoming
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		if msg.ID != 0 {
			c.mu.Lock()
			ch, ok := c.pending[msg.ID]
			if ok {
				delete(c.pending, msg.ID)
			}
			c.mu.Unlock()
			if ok {
				ch <- rawResponse{result: msg.Result, err: msg.Error}
			}
			continue
		}
		if msg.Method != "" {
			c.dispatchEvent(msg.SessionID, msg.Method, msg.Params)
		}
	}
}

func (c *Conn) dispatchEvent(sessionID, method string, params json.RawMessage) {
	c.listenersMu.RLock()
	var cbs []func(json.RawMessage)
	if byMethod, ok := c.listeners[sessionID]; ok {
		cbs = append(cbs, byMethod[method]...)
	}
	c.listenersMu.RUnlock()
	for _, cb := range cbs {
		cb(params)
	}
}

func (c *Conn) fail(err error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.closeErr = err
	pending := c.pending
	c.pending = make(map[int64]chan rawResponse)
	c.mu.Unlock()
	for _, ch := range pending {
		ch <- rawResponse{err: &ProtocolError{Code: -1, Message: "connection closed: " + err.Error()}}
	}
}

// Send issues a CDP command on the browser-level session and unmarshals the
// result into out (which may be nil).
func (c *Conn) Send(ctx context.Context, method string, params any, out any) error {
	return c.SendSession(ctx, "", method, params, out)
}

// SendSession issues a CDP command tagged with a sessionId.
func (c *Conn) SendSession(ctx context.Context, sessionID, method string, params any, out any) error {
	id := atomic.AddInt64(&c.nextID, 1)
	ch := make(chan rawResponse, 1)

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return fmt.Errorf("cdp: connection closed: %v", c.closeErr)
	}
	c.pending[id] = ch
	c.mu.Unlock()

	cmd := command{ID: id, Method: method, Params: params, SessionID: sessionID}
	data, err := json.Marshal(cmd)
	if err != nil {
		c.removePending(id)
		return err
	}
	if err := c.ws.Write(ctx, websocket.MessageText, data); err != nil {
		c.removePending(id)
		return err
	}

	select {
	case <-ctx.Done():
		c.removePending(id)
		return ctx.Err()
	case resp := <-ch:
		if resp.err != nil {
			return resp.err
		}
		if out != nil && len(resp.result) > 0 {
			return json.Unmarshal(resp.result, out)
		}
		return nil
	}
}

func (c *Conn) removePending(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

// On registers an event listener for a (sessionId, method) pair. Pass sessionId
// "" for browser-level events. Returns a function that removes the listener.
func (c *Conn) On(sessionID, method string, cb func(params json.RawMessage)) func() {
	c.listenersMu.Lock()
	defer c.listenersMu.Unlock()
	if c.listeners[sessionID] == nil {
		c.listeners[sessionID] = make(map[string][]func(json.RawMessage))
	}
	idx := len(c.listeners[sessionID][method])
	c.listeners[sessionID][method] = append(c.listeners[sessionID][method], cb)

	removed := false
	return func() {
		c.listenersMu.Lock()
		defer c.listenersMu.Unlock()
		if removed {
			return
		}
		removed = true
		cbs := c.listeners[sessionID][method]
		if idx < len(cbs) {
			cbs[idx] = func(json.RawMessage) {} // tombstone, keep indices stable
		}
	}
}

// WaitEvent blocks until an event with the given (sessionId, method) arrives or
// the context is cancelled. Returns the raw params.
func (c *Conn) WaitEvent(ctx context.Context, sessionID, method string) (json.RawMessage, error) {
	ch := make(chan json.RawMessage, 1)
	remove := c.On(sessionID, method, func(params json.RawMessage) {
		select {
		case ch <- params:
		default:
		}
	})
	defer remove()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case p := <-ch:
		return p, nil
	}
}

// Close closes the underlying WebSocket.
func (c *Conn) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.mu.Unlock()
	err := c.ws.Close(websocket.StatusNormalClosure, "")
	select {
	case <-c.done:
	case <-time.After(2 * time.Second):
	}
	return err
}

// Done returns a channel closed when the read loop exits (connection dead).
func (c *Conn) Done() <-chan struct{} { return c.done }
