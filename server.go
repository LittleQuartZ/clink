package main

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"

	gonanoid "github.com/matoous/go-nanoid/v2"
)

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
	id, err := gonanoid.Generate("ABCDEF0123456789", 6)
	if err != nil || id == "" {
		// Fallback to remote addr if generation fails
		id = c.RemoteAddr().String()
	}

	// Default username is server-controlled; not necessarily unique
	defaultName := "user_" + strings.ToLower(id)
	username := defaultName

	// Greet client and instruct on setting username
	fmt.Fprintf(c, "Welcome %s (%s)\n", username, id)
	fmt.Fprintln(c, "Use /name <username> to set your username. Allowed: [A-Za-z0-9_.-] (spaces become _)")
	// Announce join to others, exclude self
	h.msgCh <- broadcast{text: fmt.Sprintf("[join] %s (%s)", username, id), exclude: c}

	scanner := bufio.NewScanner(c)
	// Allow reasonably large lines
	scanner.Buffer(make([]byte, 0, 1024), 64*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		// Commands
		if line == "/quit" {
			break // unified leave handling below
		}
		if strings.HasPrefix(line, "/name ") {
			desired := strings.TrimSpace(strings.TrimPrefix(line, "/name "))
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
