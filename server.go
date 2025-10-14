package main

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
)

type Hub struct {
	mu      sync.Mutex
	conns   map[net.Conn]struct{}
	joinCh  chan net.Conn
	leaveCh chan net.Conn
	msgCh   chan string
}

func NewHub() *Hub {
	return &Hub{
		conns:   make(map[net.Conn]struct{}),
		joinCh:  make(chan net.Conn),
		leaveCh: make(chan net.Conn),
		msgCh:   make(chan string, 128),
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
				// newline-delimited messages
				fmt.Fprintln(c, msg)
			}
			h.mu.Unlock()
		}
	}
}

func handleConn(h *Hub, c net.Conn) {
	defer func() { h.leaveCh <- c }()
	h.joinCh <- c

	name := c.RemoteAddr().String()
	fmt.Fprintf(c, "Welcome %s\n", name)
	h.msgCh <- fmt.Sprintf("[join] %s", name)

	scanner := bufio.NewScanner(c)
	scanner.Buffer(make([]byte, 0, 1024), 64*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if line == "/quit" {
			return
		}
		h.msgCh <- fmt.Sprintf("%s: %s", name, line)
	}
	if err := scanner.Err(); err != nil {
		log.Printf("read err from %s: %v", name, err)
	}
	h.msgCh <- fmt.Sprintf("[leave] %s", name)
}

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
