package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"

	gonanoid "github.com/matoous/go-nanoid/v2"
)

var defaultMenu = []menuItem{
	{ID: "latte", Name: "Caffè Latte", Price: 4.50},
	{ID: "cap", Name: "Cappuccino", Price: 4.00},
	{ID: "esp", Name: "Espresso", Price: 3.00},
}

// order is the structure the server expects for ORDER.
type order struct {
	Name     string `json:"name"`
	ItemID   string `json:"itemId"`
	Quantity int    `json:"quantity"`
}

// broadcast represents a line to send to all connections with the ability
// to exclude a single connection (e.g., exclude self on join).
type broadcast struct {
	text    string
	exclude net.Conn
}

// Hub manages the set of connected clients and fan-out of messages.
type Hub struct {
	mu      sync.Mutex
	conns   map[net.Conn]struct{}
	joinCh  chan net.Conn
	leaveCh chan net.Conn
	msgCh   chan broadcast
}

func NewHub() *Hub {
	return &Hub{
		conns:   make(map[net.Conn]struct{}),
		joinCh:  make(chan net.Conn),
		leaveCh: make(chan net.Conn),
		msgCh:   make(chan broadcast, 128),
	}
}

func (h *Hub) Run() {
	for {
		select {
		case c := <-h.joinCh:
			h.mu.Lock()
			h.conns[c] = struct{}{}
			h.mu.Unlock()
		case c := <-h.leaveCh:
			h.mu.Lock()
			if _, ok := h.conns[c]; ok {
				delete(h.conns, c)
				_ = c.Close()
			}
			h.mu.Unlock()
		case msg := <-h.msgCh:
			h.mu.Lock()
			for c := range h.conns {
				if msg.exclude != nil && c == msg.exclude {
					continue
				}
				// Newline-delimited messages
				fmt.Fprintln(c, msg.text)
			}
			h.mu.Unlock()
		}
	}
}

// sanitizeUsername enforces server rules on allowed usernames.
// - letters, digits, '_', '-', '.' allowed
// - spaces converted to '_'
// - trimmed of leading/trailing '.', '_' or '-'
// - empty after sanitization is invalid
// - max length limited
func sanitizeUsername(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	const maxLen = 12
	var out []rune
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '_', r == '-', r == '.':
			out = append(out, r)
		case r == ' ':
			out = append(out, '_')
		default:
			// skip everything else
		}
		if len(out) >= maxLen {
			break
		}
	}
	res := strings.Trim(string(out), "._-")
	return res
}

func handleConn(h *Hub, c net.Conn) {
	defer func() { h.leaveCh <- c }()
	h.joinCh <- c

	// Generate per-connection ID
	id, err := gonanoid.Generate("abcdef0123456789", 6)
	if err != nil || id == "" {
		// Fallback to remote addr if generation fails
		id = c.RemoteAddr().String()
	}

	// Default username is server-controlled; not necessarily unique
	defaultName := "user_" + id
	username := defaultName

	// Greet client and instruct on setting username
	fmt.Fprintf(c, "Welcome %s (%s)\n", username, id)
	fmt.Fprintln(c, "Use /name <username> to set your username. Allowed: [A-Za-z0-9_.-] (spaces become _)")
	// Announce join to others, exclude self
	log.Printf("join: user=%s id=%s remote=%s", username, id, c.RemoteAddr())
	h.msgCh <- broadcast{text: fmt.Sprintf("[join] %s (%s)", username, id), exclude: c}

	scanner := bufio.NewScanner(c)
	// Allow reasonably large lines
	scanner.Buffer(make([]byte, 0, 1024), 64*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		// New protocol commands:
		// MENU -> server returns single-line JSON array of menuItem
		if strings.EqualFold(line, "MENU") {
			b, err := json.Marshal(defaultMenu)
			if err != nil {
				fmt.Fprintln(c, `[error] failed to encode menu`)
				continue
			}
			fmt.Fprintln(c, string(b))
			continue
		}

		// ORDER <json> -> server validates and replies with a single-line ack
		if strings.HasPrefix(line, "ORDER") {
			raw := strings.TrimSpace(line[len("ORDER"):])
			var ord order
			if err := json.Unmarshal([]byte(raw), &ord); err != nil {
				fmt.Fprintln(c, "[error] invalid order json")
				continue
			}
			ord.Name = strings.TrimSpace(ord.Name)
			log.Printf("ORDER parsed: name=%q itemId=%q qty=%d", ord.Name, ord.ItemID, ord.Quantity)
			if ord.Name == "" {
				fmt.Fprintln(c, "[error] missing name")
				continue
			}
			// Fallback handling: accept numeric strings or floats for quantity
			if ord.Quantity <= 0 {
				var generic map[string]any
				if err := json.Unmarshal([]byte(raw), &generic); err == nil {
					if v, ok := generic["quantity"]; ok {
						switch t := v.(type) {
						case string:
							if n, err := strconv.Atoi(strings.TrimSpace(t)); err == nil {
								ord.Quantity = n
							}
						case float64:
							ord.Quantity = int(t)
						}
					}
				}
			}
			if ord.Quantity <= 0 {
				fmt.Fprintln(c, "[error] invalid quantity")
				continue
			}
			var chosen *menuItem
			for i := range defaultMenu {
				if defaultMenu[i].ID == ord.ItemID {
					chosen = &defaultMenu[i]
					break
				}
			}
			if chosen == nil {
				fmt.Fprintln(c, "[error] unknown item")
				continue
			}

			total := float64(ord.Quantity) * chosen.Price

			h.msgCh <- broadcast{
				text: fmt.Sprintf("[order] %s (%s) ordered %d × %s ($%.2f)", username, id, ord.Quantity, chosen.Name, total),
			}

			fmt.Fprintf(c, "OK|%.2f\n", total)
			continue
		}

		// Chat commands
		if line == "/quit" {
			break // unified leave handling below
		}
		if desired, ok := strings.CutPrefix(line, "/name "); ok {
			newName := sanitizeUsername(desired)
			if newName == "" {
				fmt.Fprintln(c, "[error] invalid username")
				continue
			}
			if newName == username {
				// No change
				fmt.Fprintf(c, "[info] username unchanged: %s\n", username)
				continue
			}
			old := username
			username = newName
			// Broadcast rename to everyone (including the renamer)
			log.Printf("rename: user=%s id=%s remote=%s", username, id, c.RemoteAddr())
			h.msgCh <- broadcast{text: fmt.Sprintf("[rename] %s (%s) -> %s", old, id, username)}
			continue
		}

		// Regular chat message
		h.msgCh <- broadcast{text: fmt.Sprintf("%s (%s): %s", username, id, line)}
	}
	if err := scanner.Err(); err != nil {
		log.Printf("read err from %s (%s): %v", username, id, err)
	}

	// Single, consistent leave announcement
	log.Printf("leave: user=%s id=%s remote=%s", username, id, c.RemoteAddr())
	h.msgCh <- broadcast{text: fmt.Sprintf("[leave] %s (%s)", username, id)}
}

// startTCPServer starts a TCP chat server and never returns unless an error occurs.
func startTCPServer(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	log.Printf("TCP chat server listening on %s", ln.Addr())

	hub := NewHub()
	go hub.Run()

	for {
		c, err := ln.Accept()
		if err != nil {
			log.Printf("accept error: %v", err)
			continue
		}
		go handleConn(hub, c)
	}
}
