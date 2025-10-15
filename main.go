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

type menuItem struct {
	ID    string  `json:"id"`
	Name  string  `json:"name"`
	Price float64 `json:"price"`
}

// order represents the payload we submit back to the server.

// messages used by Bubble Tea
type (
	connectedMsg  struct{ conn net.Conn }
	menuLoadedMsg struct {
		items []menuItem
		err   error
	}
	orderSubmittedMsg struct {
		ack   string
		total float64
		err   error
	}
	broadcastMsg  string
	statusMsg     string
	serverLineMsg string
)

type FormFields struct {
	name        string
	itemID      string
	quantityStr string
	confirm     bool
}

// model holds the TUI state.
type model struct {
	host string
	conn net.Conn

	title      string
	status     string
	loading    bool
	err        error
	lastOrder  *order
	broadcasts []string

	form        *huh.Form
	formFields  *FormFields
	menu        []menuItem
	name        string
	itemID      string
	quantityStr string
	confirm     bool

	width  int
	height int

	reader             *bufio.Reader
	broadcastListening bool
	pauseBroadcast     bool
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
				m.pauseBroadcast = true
				m.status = "Submitting order..."
				return m, submitOrderCmd(m.conn, *ord, m.reader)
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
		m.reader = bufio.NewReader(m.conn)
		m.status = fmt.Sprintf("Connected to %s", m.host)

		_ = m.conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		for i := 0; i < 2; i++ {
			if _, err := m.reader.ReadString('\n'); err != nil {
				break
			}
		}
		_ = m.conn.SetReadDeadline(time.Time{})

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

		m.form = m.buildForm()
		return m, m.form.Init()

	case orderSubmittedMsg:
		m.loading = false
		m.pauseBroadcast = false
		if msg.err != nil {
			m.err = msg.err
			m.status = "Order submission failed."
			if m.broadcastListening {
				return m, listenForBroadcastsCmd(m.conn, m.reader)
			}
			return m, nil
		}
		m.err = nil
		if msg.total > 0 {
			m.status = fmt.Sprintf("Order submitted. Total: $%.2f", msg.total)

			if !m.broadcastListening {
				m.broadcastListening = true
				return m, listenForBroadcastsCmd(m.conn, m.reader)
			}
			return m, listenForBroadcastsCmd(m.conn, m.reader)
		} else if msg.ack != "" {
			m.status = fmt.Sprintf("Order submitted. Server says: %s", msg.ack)
		}
		if m.broadcastListening {
			return m, listenForBroadcastsCmd(m.conn, m.reader)
		}
		return m, nil

	case broadcastMsg:
		msgText := string(msg)
		if msgText != "" && strings.HasPrefix(msgText, "[order]") {
			m.broadcasts = append(m.broadcasts, msgText)
			if len(m.broadcasts) > 10 {
				m.broadcasts = m.broadcasts[1:]
			}
		}
		if m.pauseBroadcast {
			time.Sleep(50 * time.Millisecond)
		}
		return m, listenForBroadcastsCmd(m.conn, m.reader)
	case statusMsg:
		msgStr := string(msg)
		m.status = msgStr
		if strings.Contains(msgStr, "Connection closed") {
			if m.conn != nil {
				_ = m.conn.Close()
				m.conn = nil
			}
			m.broadcastListening = false
			m.reader = nil
		}
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
			m.broadcastListening = false
			m.reader = nil
			m.status = "Reconnecting..."
			return m, connectCmd(m.host)
		case "n":
			if m.loading || m.form != nil {
				return m, nil
			}
			if m.conn == nil {
				m.status = "Not connected. Press 'r' to reconnect."
				return m, nil
			}
			m.err = nil
			if len(m.menu) > 0 {
				m.form = m.buildForm()
				return m, m.form.Init()
			}
			m.loading = true
			m.status = "Loading menu..."
			return m, fetchMenuCmd(m.conn, m.reader)
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	}

	return m, nil
}

func (m model) renderHeader() string {
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	hostStyle := lipgloss.NewStyle().Faint(true)

	title := titleStyle.Render(m.title)
	host := hostStyle.Render(m.host)

	header := lipgloss.JoinVertical(lipgloss.Center, title, host)
	return lipgloss.NewStyle().Width(m.width).Align(lipgloss.Center).Render(header)
}

func (m model) renderLeftColumn() string {
	lines := []string{}

	if m.loading {
		lines = append(lines, "Status: "+lipgloss.NewStyle().Foreground(lipgloss.Color("178")).Render("Loading..."))
	} else if m.status != "" {
		lines = append(lines, "Status: "+m.status)
	}

	if m.err != nil {
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render(fmt.Sprintf("Error: %v", m.err)))
	}

	if m.lastOrder != nil {
		lines = append(lines, "", lipgloss.NewStyle().Bold(true).Render("Last Order:"))
		lines = append(lines, fmt.Sprintf("  Name: %s", m.lastOrder.Name))
		var label string
		for _, it := range m.menu {
			if it.ID == m.lastOrder.ItemID {
				label = it.Name
				break
			}
		}
		if label != "" {
			lines = append(lines, fmt.Sprintf("  Item: %s", label))
		} else {
			lines = append(lines, fmt.Sprintf("  Item: %s", m.lastOrder.ItemID))
		}
		lines = append(lines, fmt.Sprintf("  Quantity: %d", m.lastOrder.Quantity))
	}

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)
	return lipgloss.NewStyle().
		Width(m.width/2 - 2).
		Height(m.height - 6).
		Padding(1).
		Render(content)
}

func (m model) renderRightColumn() string {
	lines := []string{}
	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	lines = append(lines, headerStyle.Render("Recent Orders:"))
	lines = append(lines, "")

	if len(m.broadcasts) == 0 {
		lines = append(lines, lipgloss.NewStyle().Faint(true).Render("No orders yet..."))
	} else {
		bulletStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("141"))
		nameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("86")).Bold(true)
		itemStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("117"))
		priceStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Bold(true)

		for _, b := range m.broadcasts {
			msg := strings.TrimPrefix(b, "[order] ")
			parts := strings.SplitN(msg, " ordered ", 2)
			if len(parts) == 2 {
				customer := parts[0]
				orderDetails := parts[1]

				line := fmt.Sprintf("%s %s ordered %s",
					bulletStyle.Render("•"),
					nameStyle.Render(customer),
					itemStyle.Render(orderDetails))

				if idx := strings.Index(orderDetails, "($"); idx != -1 {
					priceStart := idx
					priceEnd := strings.Index(orderDetails[priceStart:], ")")
					if priceEnd != -1 {
						priceEnd += priceStart + 1
						beforePrice := orderDetails[:priceStart]
						priceText := orderDetails[priceStart:priceEnd]

						line = fmt.Sprintf("%s %s ordered %s %s",
							bulletStyle.Render("•"),
							nameStyle.Render(customer),
							itemStyle.Render(beforePrice),
							priceStyle.Render(priceText))
					}
				}

				lines = append(lines, line)
			}
		}
	}

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)
	return lipgloss.NewStyle().
		Width(m.width/2 - 2).
		Height(m.height - 6).
		Padding(1).
		Render(content)
}

func (m model) renderFooter() string {
	connStatus := ""
	if m.conn != nil {
		connStatus = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Render("● Connected")
	} else {
		connStatus = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render("● Disconnected")
	}

	controls := lipgloss.NewStyle().Faint(true).Render("n: New Order  r: Reconnect  q: Quit")

	leftSide := connStatus
	rightSide := controls

	footer := lipgloss.JoinHorizontal(lipgloss.Top,
		lipgloss.NewStyle().Width(m.width/2).Render(leftSide),
		lipgloss.NewStyle().Width(m.width/2).Align(lipgloss.Right).Render(rightSide),
	)

	return lipgloss.NewStyle().Width(m.width).Render(footer)
}

func (m model) View() string {
	if m.form != nil {
		return m.form.View()
	}

	if m.width == 0 || m.height == 0 {
		return "Loading..."
	}

	header := m.renderHeader()

	leftCol := m.renderLeftColumn()
	rightCol := m.renderRightColumn()
	body := lipgloss.JoinHorizontal(lipgloss.Top, leftCol, rightCol)

	footer := m.renderFooter()

	return lipgloss.JoinVertical(lipgloss.Left,
		header,
		"",
		body,
		"",
		footer,
	)
}

// buildForm constructs the order form: Input (name) -> Select (menu) -> Input (qty) -> Confirm.
func (m *model) buildForm() *huh.Form {
	opts := make([]huh.Option[string], 0, len(m.menu))
	for _, it := range m.menu {
		opts = append(opts, huh.NewOption(fmt.Sprintf("%s - $%.2f", it.Name, it.Price), it.ID))
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

		return connectedMsg{conn: conn}
	}
}

// fetchMenuCmd asks the server for a menu via the TCP connection.
// Protocol (proposed):
// - client: "MENU\n"
// - server: single line JSON array: [{"id":"x","name":"..."}]\n
func fetchMenuCmd(conn net.Conn, reader *bufio.Reader) tea.Cmd {
	return func() tea.Msg {
		if conn == nil || reader == nil {
			return menuLoadedMsg{err: errors.New("not connected")}
		}

		if _, err := fmt.Fprintln(conn, "MENU"); err != nil {
			return menuLoadedMsg{err: fmt.Errorf("send MENU: %w", err)}
		}

		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		defer func() { _ = conn.SetReadDeadline(time.Time{}) }()

		line, err := reader.ReadString('\n')
		if err != nil {
			return menuLoadedMsg{err: fmt.Errorf("read MENU: %w", err)}
		}
		line = strings.TrimRight(line, "\r\n")
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
func submitOrderCmd(conn net.Conn, ord order, reader *bufio.Reader) tea.Cmd {
	return func() tea.Msg {
		if conn == nil || reader == nil {
			return orderSubmittedMsg{err: errors.New("not connected")}
		}
		b, err := json.Marshal(ord)
		if err != nil {
			return orderSubmittedMsg{err: fmt.Errorf("marshal order: %w", err)}
		}

		if _, err := fmt.Fprintf(conn, "ORDER %s\n", string(b)); err != nil {
			return orderSubmittedMsg{err: fmt.Errorf("send ORDER: %w", err)}
		}

		time.Sleep(150 * time.Millisecond)

		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		defer func() { _ = conn.SetReadDeadline(time.Time{}) }()

		line, err := reader.ReadString('\n')
		if err != nil {
			return orderSubmittedMsg{err: fmt.Errorf("read ORDER ack: %w", err)}
		}
		line = strings.TrimRight(line, "\r\n")
		parts := strings.Split(line, "|")
		ack := parts[0]
		var total float64
		if len(parts) > 1 {
			if t, err := strconv.ParseFloat(parts[1], 64); err == nil {
				total = t
			}
		}
		return orderSubmittedMsg{ack: ack, total: total}
	}
}

func listenForBroadcastsCmd(conn net.Conn, reader *bufio.Reader) tea.Cmd {
	return func() tea.Msg {
		defer func() {
			if r := recover(); r != nil {
				return
			}
		}()
		if conn == nil || reader == nil {
			return nil
		}

		_ = conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		line, err := reader.ReadString('\n')
		_ = conn.SetReadDeadline(time.Time{})

		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				return broadcastMsg("")
			}
			return statusMsg(fmt.Sprintf("Connection closed: %v", err))
		}
		return broadcastMsg(strings.TrimRight(line, "\r\n"))
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
