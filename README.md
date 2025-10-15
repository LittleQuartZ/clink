# Clink - TCP Socket-Based Order System

## Overview

Clink is a real-time order management system demonstrating TCP socket programming concepts in Go. The application consists of a TCP server that manages client connections and broadcasts order updates, and a Terminal User Interface (TUI) client that connects to the server to place orders and receive real-time updates from all connected clients.

**Main Topic:** Socket Programming with TCP in Go

---

## Architecture

### Components

1. **TCP Server** (`server.go`) - Multi-client socket server with broadcast capability
2. **TUI Client** (`main.go`) - Interactive terminal client using TCP sockets
3. **Protocol** - Custom text-based protocol over TCP

---

## Socket Programming Concepts Demonstrated

### 1. TCP Server Socket Creation

**Location:** `server.go:252-256`

```go
func startTCPServer(addr string) error {
    ln, err := net.Listen("tcp", addr)
    if err != nil {
        return err
    }
```

- Uses `net.Listen()` to create a TCP listener socket
- Binds to the specified address (default: `localhost:9000`)
- Returns a `net.Listener` that can accept incoming connections

---

### 2. Accepting Client Connections

**Location:** `server.go:261-268`

```go
for {
    c, err := ln.Accept()
    if err != nil {
        log.Printf("accept error: %v", err)
        continue
    }
    go handleConn(hub, c)
}
```

- `Accept()` blocks until a client connects
- Each connection is handled in a separate goroutine
- This enables concurrent handling of multiple clients

---

### 3. Client Socket Connection

**Location:** `main.go:484-493`

```go
func connectCmd(addr string) tea.Cmd {
    return func() tea.Msg {
        conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
        if err != nil {
            return statusMsg(fmt.Sprintf("Connect failed: %v", err))
        }
        return connectedMsg{conn: conn}
    }
}
```

- Uses `net.DialTimeout()` to establish TCP connection
- Includes 3-second timeout to prevent indefinite blocking
- Returns `net.Conn` interface for bidirectional communication

---

### 4. Buffered Reading from Socket

**Location:** `main.go:148-150`

```go
case connectedMsg:
    m.conn = msg.conn
    m.reader = bufio.NewReader(m.conn)
```

**Why Buffered Reading?**
- Raw socket reads are byte-level and inefficient
- `bufio.Reader` provides buffering and line-reading capabilities
- **Critical:** Single `bufio.Reader` instance per connection prevents data corruption
- Multiple readers on the same socket would compete for bytes

---

### 5. Reading Greeting Messages

**Location:** `main.go:153-159`

```go
_ = m.conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
for i := 0; i < 2; i++ {
    if _, err := m.reader.ReadString('\n'); err != nil {
        break
    }
}
_ = m.conn.SetReadDeadline(time.Time{})
```

- Server sends 2 greeting lines upon connection (lines 131-132 in `server.go`)
- Client consumes these to prevent interference with protocol messages
- **Socket Deadline:** Temporary 500ms timeout prevents indefinite blocking
- Deadline is reset to zero (no timeout) after consuming greetings

---

### 6. Writing to Socket

**Location:** `main.go:506`, `main.go:544`

```go
// Sending MENU request
fmt.Fprintln(conn, "MENU")

// Sending ORDER request
fmt.Fprintf(conn, "ORDER %s\n", string(b))
```

**Location:** `server.go:155`, `server.go:211`

```go
// Server responses
fmt.Fprintln(c, string(b))           // MENU response
fmt.Fprintf(c, "OK|%.2f\n", total)   // ORDER response
```

- `fmt.Fprintln()` and `fmt.Fprintf()` write to any `io.Writer`, including sockets
- Newline-delimited protocol (each message ends with `\n`)
- Text-based protocol for simplicity and debuggability

---

### 7. Socket Read Deadlines (Timeouts)

**Location:** `main.go:510-511`, `main.go:550-551`, `main.go:581-583`

```go
// Set 3-second timeout for MENU
_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
defer func() { _ = conn.SetReadDeadline(time.Time{}) }()

// Set 5-second timeout for ORDER
_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))

// Set 100ms timeout for broadcasts (polling)
_ = conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
```

**Why Different Timeouts?**
- **MENU/ORDER (3-5s):** Synchronous request-response, expect immediate reply
- **Broadcasts (100ms):** Asynchronous polling loop, short timeout prevents blocking
- Deadlines prevent reads from blocking forever if server stops responding

**Deadline vs Timeout:**
- `SetReadDeadline()` sets an absolute time
- After deadline expires, read returns `net.Error` with `Timeout() == true`

---

### 8. Handling Timeout Errors

**Location:** `main.go:585-589`

```go
if err != nil {
    if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
        return broadcastMsg("")  // Timeout is expected, continue polling
    }
    return statusMsg(fmt.Sprintf("Connection closed: %v", err))
}
```

- Type assertion to check if error is `net.Error`
- Distinguish between timeout (expected) and connection failure
- Timeouts in broadcast loop are normal behavior (polling mechanism)

---

### 9. Concurrent Socket Communication Challenge

**The Problem:** Race condition between synchronous requests and asynchronous broadcasts

**Location:** `main.go:130`, `main.go:548`, `main.go:213-215`

```go
// Before ORDER: Pause broadcast listener
m.pauseBroadcast = true

// Wait for broadcast listener's current read to timeout
time.Sleep(150 * time.Millisecond)

// Broadcast handler: Slow down polling when paused
if m.pauseBroadcast {
    time.Sleep(50 * time.Millisecond)
}
```

**Why This Is Needed:**
- Single TCP socket shared between request-response and broadcast listening
- Only one goroutine can read from socket at a time
- Without coordination, broadcast listener might consume ORDER response
- Solution: Coordinate reads using pause flag and timeouts

**Timeline Without Coordination:**
```
Time    Action
t=0     ORDER sent
t=1     Broadcast listener reads "OK|9.00" (WRONG!)
t=3000  submitOrderCmd times out (never got response)
```

**Timeline With Coordination:**
```
Time    Action
t=0     pauseBroadcast = true
t=0     ORDER sent
t=150   Broadcast listener's 100ms timeout expires
t=150   Broadcast listener sees pause, sleeps 50ms
t=150   submitOrderCmd reads "OK|9.00" (SUCCESS!)
t=200   Broadcast listener resumes with pause = false
```

---

### 10. Hub Pattern for Broadcasting

**Location:** `server.go:36-80`

```go
type Hub struct {
    mu      sync.Mutex
    conns   map[net.Conn]struct{}
    joinCh  chan net.Conn
    leaveCh chan net.Conn
    msgCh   chan broadcast
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
                fmt.Fprintln(c, msg.text)  // Write to each socket
            }
            h.mu.Unlock()
        }
    }
}
```

**Pattern Benefits:**
- Centralized connection management
- Thread-safe access to connection map using mutex
- Fan-out broadcast to all connected sockets
- Optional exclusion (don't echo back to sender)

**Broadcasting an Order:** `server.go:207-209`
```go
h.msgCh <- broadcast{
    text: fmt.Sprintf("[order] %s ordered %d × %s ($%.2f)", 
                      ord.Name, ord.Quantity, chosen.Name, total),
}
```

---

### 11. Per-Connection Handler

**Location:** `server.go:115-248`

```go
func handleConn(h *Hub, c net.Conn) {
    defer func() { h.leaveCh <- c }()
    h.joinCh <- c
    
    scanner := bufio.NewScanner(c)
    scanner.Buffer(make([]byte, 0, 1024), 64*1024)
    
    for scanner.Scan() {
        line := strings.TrimSpace(scanner.Text())
        // Protocol handling...
    }
}
```

**Key Points:**
- Runs in separate goroutine per connection
- `defer` ensures cleanup when connection closes
- Uses `bufio.Scanner` for line-by-line reading
- Handles multiple protocol commands: `MENU`, `ORDER`, `/name`, `/quit`

---

### 12. Connection Lifecycle

**Server Side:**
1. `ln.Accept()` - Accept incoming connection (`server.go:262`)
2. `h.joinCh <- c` - Register connection in hub (`server.go:117`)
3. Send greeting messages (`server.go:131-132`)
4. Process commands in loop (`server.go:141-240`)
5. `h.leaveCh <- c` - Unregister and close (`server.go:116`)

**Client Side:**
1. `net.DialTimeout()` - Establish connection (`main.go:487`)
2. Create `bufio.Reader` (`main.go:150`)
3. Consume greeting messages (`main.go:153-159`)
4. Send protocol commands (`MENU`, `ORDER`)
5. Listen for broadcasts (`main.go:570-593`)
6. `conn.Close()` - Close on quit (`main.go:234`)

---

## Protocol Specification

### Text-Based Protocol over TCP

All messages are newline-delimited (`\n`).

#### Client → Server

**1. MENU Request**
- Format: `MENU\n`
- Location: `main.go:506`
- Server handler: `server.go:149-157`
- Response: JSON array of menu items

**Example:**
```
Client: MENU
Server: [{"id":"latte","name":"Caffè Latte","price":4.5},...]
```

**2. ORDER Request**
- Format: `ORDER <json>\n`
- Location: `main.go:544`
- Server handler: `server.go:160-213`
- Response: `OK|<total>\n`

**Example:**
```
Client: ORDER {"name":"Alice","itemId":"latte","quantity":2}
Server: OK|9.00
```

#### Server → Client (Broadcasts)

**1. Order Broadcast**
- Format: `[order] <name> ordered <qty> × <item> ($<total>)\n`
- Location: `server.go:207-209`
- Client handler: `main.go:205-216`

**Example:**
```
[order] Alice ordered 2 × Caffè Latte ($9.00)
```

**2. Join/Leave Broadcasts**
- Format: `[join] <username> (<id>)\n` or `[leave] <username> (<id>)\n`
- Location: `server.go:135`, `server.go:247`

---

## Application Flow

### Server Startup Flow

```
1. main() [server.go:595]
   └─ Parse flags: --server
   
2. startTCPServer() [server.go:251]
   └─ net.Listen("tcp", addr)
   
3. hub.Run() [server.go:54]
   └─ Goroutine: Handle join/leave/broadcast channels
   
4. Accept loop [server.go:261-268]
   └─ For each connection:
      └─ go handleConn(hub, conn)
         ├─ Register: hub.joinCh <- conn
         ├─ Send greeting [131-132]
         ├─ Process commands [141-240]
         │  ├─ MENU → JSON response [149-157]
         │  ├─ ORDER → Validate, broadcast, ack [160-213]
         │  └─ /name, /quit [216-236]
         └─ Cleanup: hub.leaveCh <- conn [116]
```

---

### Client Startup Flow

```
1. main() [main.go:595]
   └─ tea.NewProgram(initialModel(host))
   
2. Init() [main.go:88-91]
   └─ Return connectCmd(host)
   
3. connectCmd() [main.go:484-493]
   └─ net.DialTimeout("tcp", addr, 3s)
   └─ Return connectedMsg{conn}
   
4. Update(connectedMsg) [main.go:148-161]
   ├─ Create bufio.Reader(conn)
   ├─ Consume 2 greeting lines
   └─ Status: "Connected"
```

---

### Order Submission Flow (Client)

```
1. User presses 'n' [main.go:247]
   └─ If menu cached: Show form
   └─ Else: fetchMenuCmd()
   
2. fetchMenuCmd() [main.go:500-527]
   ├─ Write: "MENU\n"
   ├─ SetReadDeadline(3s)
   └─ Read JSON response
   
3. Update(menuLoadedMsg) [main.go:163-175]
   └─ Build and show order form
   
4. User fills form [name, item, quantity, confirm]
   └─ Form completed [main.go:107-135]
   
5. If confirmed:
   ├─ Set pauseBroadcast = true [130]
   └─ submitOrderCmd() [534-567]
      ├─ Marshal order to JSON
      ├─ Write: "ORDER <json>\n"
      ├─ Sleep 150ms (coordination)
      ├─ SetReadDeadline(5s)
      └─ Read: "OK|<total>\n"
      
6. Update(orderSubmittedMsg) [main.go:177-203]
   ├─ Set pauseBroadcast = false
   ├─ Display success status
   ├─ If first order:
   │  └─ Start broadcast listener
   └─ Else:
      └─ Resume broadcast listener
```

---

### Broadcast Listening Flow (Client)

```
1. Start after first order [main.go:192-194]
   └─ Set broadcastListening = true
   
2. listenForBroadcastsCmd() [main.go:570-593]
   ├─ SetReadDeadline(100ms)  # Poll with timeout
   ├─ line, err := reader.ReadString('\n')
   └─ Three outcomes:
      ├─ Timeout → Return broadcastMsg("")
      ├─ Error → Connection closed
      └─ Success → Return broadcastMsg(line)
      
3. Update(broadcastMsg) [main.go:205-216]
   ├─ If "[order]" prefix:
   │  └─ Append to broadcasts list (max 10)
   ├─ If pauseBroadcast:
   │  └─ Sleep 50ms (slow down polling)
   └─ Restart: listenForBroadcastsCmd()
```

**Polling Loop Visualization:**
```
Time (ms)  Action
0          SetReadDeadline(+100ms)
0-100      Blocking read...
100        Timeout → broadcastMsg("")
100        Handle message, restart
100        SetReadDeadline(+100ms)
100-200    Blocking read...
150        [Data arrives: "[order] Alice..."]
150        Success → broadcastMsg("[order]...")
150        Handle message, append to list
150        Restart loop
```

---

### Order Processing Flow (Server)

```
1. Receive: "ORDER <json>\n" [server.go:160]
   
2. Parse and validate [server.go:161-203]
   ├─ json.Unmarshal(raw, &ord)
   ├─ Check: ord.Name != ""
   ├─ Check: ord.Quantity > 0
   └─ Lookup: Find menuItem by ord.ItemID
   
3. Calculate total [server.go:205]
   └─ total = quantity × price
   
4. Broadcast to all clients [server.go:207-209]
   └─ hub.msgCh <- broadcast{...}
      └─ Hub writes to ALL sockets [server.go:68-77]
         └─ fmt.Fprintln(c, msg.text)
   
5. Acknowledge to sender [server.go:211]
   └─ Write: "OK|<total>\n"
```

---

## Key Socket Programming Challenges & Solutions

### Challenge 1: Single Socket, Dual Purpose

**Problem:** Same socket used for:
- Synchronous request-response (MENU, ORDER)
- Asynchronous broadcast receiving

**Solution:** 
- Broadcast listener uses short (100ms) timeouts
- Before synchronous requests: pause broadcast polling
- Coordination via `pauseBroadcast` flag and sleep delays
- Locations: `main.go:130`, `main.go:548`, `main.go:213-215`

---

### Challenge 2: Buffered Reader Data Corruption

**Problem:** Multiple `bufio.Reader` instances on same socket would:
- Compete for bytes from socket buffer
- Corrupt message boundaries
- Cause incomplete/split messages

**Solution:**
- Single `bufio.Reader` created once at connection (`main.go:150`)
- Shared across all read operations
- Passed to all command functions

---

### Challenge 3: Greeting Message Interference

**Problem:** Server sends 2 greeting lines on connect
- Would be parsed as protocol responses
- MENU/ORDER reads would get greeting instead of response

**Solution:**
- Consume greetings immediately after connect (`main.go:153-159`)
- Use short deadline (500ms) to prevent blocking
- Clear socket buffer before protocol starts

---

### Challenge 4: Connection Cleanup

**Problem:** Connections must be cleaned up on:
- Client disconnect
- Server shutdown
- Network errors

**Solution (Server):**
- `defer func() { h.leaveCh <- c }()` guarantees cleanup (`server.go:116`)
- Hub centralizes close logic (`server.go:61-67`)

**Solution (Client):**
- Detect "Connection closed" in status messages (`main.go:220-227`)
- Reset all socket-related state: `conn`, `reader`, `broadcastListening`

---

## Summary

This application demonstrates:

1. **TCP Client-Server Architecture**
   - Server: `net.Listen()`, `Accept()`, per-connection goroutines
   - Client: `net.DialTimeout()`, shared connection

2. **Custom Text Protocol**
   - Newline-delimited messages
   - Request-response (MENU, ORDER)
   - Server-initiated broadcasts

3. **Concurrent Socket Communication**
   - Multiple clients handled concurrently (server)
   - Synchronous + asynchronous reads on single socket (client)

4. **Socket I/O Techniques**
   - Buffered reading with `bufio.Reader`
   - Read deadlines for timeouts
   - Formatted writing with `fmt.Fprintf()`

5. **Broadcast Pattern**
   - Hub with channels for thread-safe fan-out
   - Write to multiple sockets from single goroutine

6. **Real-World Challenges**
   - Read coordination between sync/async operations
   - Connection lifecycle management
   - Error handling and recovery

---

## Running the Application

**Start Server:**
```bash
go run . -server -host localhost:9000
```

**Start Client(s):**
```bash
go run . -host localhost:9000
```

**Client Controls:**
- `n` - New order (loads menu if needed)
- `r` - Reconnect
- `q` - Quit

---

## File Structure

```
clink/
├── main.go       # TUI client with socket operations
├── server.go     # TCP server with hub pattern
└── README.md     # This documentation
```
