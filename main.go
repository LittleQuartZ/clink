package main

import (
	"bufio"

	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

// menuItem represents one option in the server-provided menu.
// Expected JSON (one line): [{"id":"latte","name":"Caffè Latte"}, ...]

// order represents the payload we submit back to the server.

// messages used by Bubble Tea
type (
	connectedMsg  struct{ conn net.Conn }
	menuLoadedMsg struct {
		items []menuItem
		err   error
	}
	orderSubmittedMsg struct {
		ack string
		err error
	}
	statusMsg string
)

type FormFields struct {
	name        string
	itemID      string
	quantityStr string
	confirm     bool
}

// model holds the TUI state.
type model struct {
	// connection
	host string
	conn net.Conn

	// UI
	title     string
	status    string
	loading   bool
	err       error
	lastOrder *order

	// form
	form        *huh.Form
	formFields  *FormFields
	menu        []menuItem
	name        string
	itemID      string
	quantityStr string
	confirm     bool
}

// initialModel creates a base model.
func initialModel(host string) model {
	return model{
		host:       host,
		title:      "Order Console",
		formFields: &FormFields{},
	}
}

func (m model) Init() tea.Cmd {
	// Connect on startup
	return connectCmd(m.host)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	// If a form is active, delegate to it first.
	if m.form != nil {
		var cmd tea.Cmd
		form, cmd := m.form.Update(msg)

		if f, ok := form.(*huh.Form); ok {
			m.form = f
			cmds = append(cmds, cmd)
		}

		if m.form.State == huh.StateCompleted {
			// Parse and submit order if confirmed.
			qty, err := strconv.Atoi(strings.TrimSpace(m.formFields.quantityStr))
			if err != nil || qty <= 0 {
				m.err = fmt.Errorf("invalid quantity: %v", m.formFields.quantityStr)
				m.form = nil
				return m, nil
			}
			ord := &order{
				Name:     strings.TrimSpace(m.formFields.name),
				ItemID:   m.formFields.itemID,
				Quantity: qty,
			}
			m.lastOrder = ord
			m.form = nil

			if m.formFields.confirm {
				if m.conn == nil {
					m.status = "Not connected. Unable to submit order."
					return m, nil
				}
				m.err = nil
				m.loading = true
				m.status = "Submitting order..."
				return m, submitOrderCmd(m.conn, *ord)
			}
			m.status = "Order canceled."
			return m, cmd
		}

		if m.form.State == huh.StateAborted {
			m.status = "Order form aborted."
			m.form = nil
			return m, cmd
		}

		return m, tea.Batch(cmds...)
	}

	switch msg := msg.(type) {
	case connectedMsg:
		m.conn = msg.conn
		m.status = fmt.Sprintf("Connected to %s", m.host)
		return m, nil

	case menuLoadedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			m.status = "Failed to load menu."
			return m, nil
		}
		m.err = nil
		m.menu = msg.items
		m.status = "Menu loaded."
		// Open the form now that we have a menu.
		m.form = m.buildForm()
		return m, m.form.Init()

	case orderSubmittedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			m.status = "Order submission failed."
			return m, nil
		}
		m.err = nil
		if msg.ack != "" {
			m.status = fmt.Sprintf("Order submitted. Server says: %s", msg.ack)
		} else {
			m.status = "Order submitted."
		}
		return m, nil

	case statusMsg:
		m.status = string(msg)
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			if m.conn != nil {
				_ = m.conn.Close()
			}
			return m, tea.Quit
		case "r":
			// Reconnect
			if m.conn != nil {
				_ = m.conn.Close()
				m.conn = nil
			}
			m.status = "Reconnecting..."
			return m, connectCmd(m.host)
		case "n":
			// Start a new order
			if m.loading || m.form != nil {
				return m, nil
			}
			if m.conn == nil {
				m.status = "Not connected. Press 'r' to reconnect."
				return m, nil
			}
			m.err = nil
			m.loading = true
			m.status = "Loading menu..."
			return m, fetchMenuCmd(m.conn)
		}

	case tea.WindowSizeMsg:
		// No dynamic layout needed, handled in View.
	}

	return m, nil
}

func (m model) View() string {
	// Basic centered title and instructions
	w := lipgloss.NewStyle().Width(80)
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212")).Render(m.title)
	host := lipgloss.NewStyle().Faint(true).Render(m.host)

	lines := []string{
		w.Align(lipgloss.Center).Render(title),
		w.Align(lipgloss.Center).Render(host),
		"",
		"Controls:",
		"- n: New order",
		"- r: Reconnect",
		"- q: Quit",
		"",
	}

	if m.loading {
		lines = append(lines, "Status: "+lipgloss.NewStyle().Foreground(lipgloss.Color("178")).Render("Loading..."))
	} else if m.status != "" {
		lines = append(lines, "Status: "+m.status)
	}

	if m.err != nil {
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render(fmt.Sprintf("Error: %v", m.err)))
	}

	if m.lastOrder != nil {
		lines = append(lines, "", "Last order:")
		lines = append(lines, fmt.Sprintf("- Name: %s", m.lastOrder.Name))
		// Map selected item label for display
		var label string
		for _, it := range m.menu {
			if it.ID == m.lastOrder.ItemID {
				label = it.Name
				break
			}
		}
		if label != "" {
			lines = append(lines, fmt.Sprintf("- Item: %s (%s)", label, m.lastOrder.ItemID))
		} else {
			lines = append(lines, fmt.Sprintf("- Item: %s", m.lastOrder.ItemID))
		}
		lines = append(lines, fmt.Sprintf("- Quantity: %d", m.lastOrder.Quantity))
	}

	if m.form != nil {
		// When the form is active, render only the form (full screen)
		return m.form.View()
	}

	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

// buildForm constructs the order form: Input (name) -> Select (menu) -> Input (qty) -> Confirm.
func (m *model) buildForm() *huh.Form {
	// Convert menu to huh options
	opts := make([]huh.Option[string], 0, len(m.menu))
	for _, it := range m.menu {
		opts = append(opts, huh.NewOption(it.Name, it.ID))
	}

	// Reset bound fields for a fresh form
	m.formFields.name = ""
	m.formFields.itemID = ""
	m.formFields.quantityStr = ""
	m.formFields.confirm = false

	f := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Your name").
				Prompt("> ").
				Placeholder("Jane Doe").
				Value(&m.formFields.name).
				Validate(func(s string) error {
					if strings.TrimSpace(s) == "" {
						return errors.New("name is required")
					}
					return nil
				}),
			huh.NewSelect[string]().
				Title("Menu item").
				Options(opts...).
				Value(&m.formFields.itemID).
				Validate(func(v string) error {
					if v == "" {
						return errors.New("please select a menu item")
					}
					return nil
				}),
			huh.NewInput().
				Title("Quantity").
				Prompt("> ").
				Placeholder("1").
				Value(&m.formFields.quantityStr).
				Validate(func(s string) error {
					n, err := strconv.Atoi(strings.TrimSpace(s))
					if err != nil || n <= 0 {
						return errors.New("enter a positive integer")
					}
					return nil
				}),
			huh.NewConfirm().
				Title("Place order?").
				Affirmative("Yes").
				Negative("No").
				Value(&m.formFields.confirm),
		),
	)

	return f
}

// connectCmd connects to the TCP server.
func connectCmd(addr string) tea.Cmd {
	return func() tea.Msg {
		conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
		if err != nil {
			return statusMsg(fmt.Sprintf("Connect failed: %v", err))
		}
		// Try to read up to two greeting lines with short deadline (optional).
		_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		br := bufio.NewReader(conn)
		for i := 0; i < 2; i++ {
			if _, err := br.ReadString('\n'); err != nil {
				break
			}
		}
		_ = conn.SetReadDeadline(time.Time{})

		return connectedMsg{conn: conn}
	}
}

// fetchMenuCmd asks the server for a menu via the TCP connection.
// Protocol (proposed):
// - client: "MENU\n"
// - server: single line JSON array: [{"id":"x","name":"..."}]\n
func fetchMenuCmd(conn net.Conn) tea.Cmd {
	return func() tea.Msg {
		if conn == nil {
			return menuLoadedMsg{err: errors.New("not connected")}
		}

		// Send request
		if _, err := fmt.Fprintln(conn, "MENU"); err != nil {
			return menuLoadedMsg{err: fmt.Errorf("send MENU: %w", err)}
		}

		// Read single JSON line
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		defer func() { _ = conn.SetReadDeadline(time.Time{}) }()
		r := bufio.NewReader(conn)
		line, err := r.ReadString('\n')
		if err != nil {
			return menuLoadedMsg{err: fmt.Errorf("read MENU: %w", err)}
		}
		line = strings.TrimRight(line, "\r\n")
		// If the server sent an error-ish line, surface it.
		if strings.HasPrefix(line, "[error]") {
			return menuLoadedMsg{err: fmt.Errorf("server: %s", line)}
		}

		var items []menuItem
		if err := json.Unmarshal([]byte(line), &items); err != nil {
			return menuLoadedMsg{err: fmt.Errorf("invalid menu JSON: %w", err)}
		}
		return menuLoadedMsg{items: items}
	}
}

// submitOrderCmd sends the order over TCP.
// Protocol (proposed):
// - client: "ORDER <json>\n"
// - server: a single line acknowledgement (freeform), e.g. "OK\n"
func submitOrderCmd(conn net.Conn, ord order) tea.Cmd {
	return func() tea.Msg {
		if conn == nil {
			return orderSubmittedMsg{err: errors.New("not connected")}
		}
		b, err := json.Marshal(ord)
		if err != nil {
			return orderSubmittedMsg{err: fmt.Errorf("marshal order: %w", err)}
		}

		if _, err := fmt.Fprintf(conn, "ORDER %s\n", string(b)); err != nil {
			return orderSubmittedMsg{err: fmt.Errorf("send ORDER: %w", err)}
		}

		// Read single-line ack
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		defer func() { _ = conn.SetReadDeadline(time.Time{}) }()
		r := bufio.NewReader(conn)
		line, err := r.ReadString('\n')
		if err != nil {
			return orderSubmittedMsg{err: fmt.Errorf("read ORDER ack: %w", err)}
		}
		return orderSubmittedMsg{ack: strings.TrimRight(line, "\r\n")}
	}
}

func main() {
	var (
		host       string
		serverOnly bool
	)
	flag.StringVar(&host, "host", "localhost:9000", "host:port to connect to or bind the server on")
	flag.BoolVar(&serverOnly, "server", false, "run only the server")
	flag.Parse()

	// If requested, start the TCP server (chat server as-is).
	if serverOnly {
		if err := startTCPServer(host); err != nil {
			fmt.Println("Server error:", err)
		}
		return
	}

	// Client TUI
	m := initialModel(host)
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Println("error:", err)
	}
}
