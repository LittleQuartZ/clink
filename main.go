package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type netMsg string
type connectedMsg struct{ conn net.Conn }
type disconnectedMsg struct{}
type errorMsg struct{ err error }

type model struct {
	vp       viewport.Model
	input    textarea.Model
	messages []string

	conn   net.Conn
	server string
	err    error
}

func initialModel(serverAddr string) model {
	vp := viewport.New(80, 20)
	vp.Style = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).Padding(0, 1).BorderForeground(lipgloss.Color("#bada55"))

	ta := textarea.New()
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.Placeholder = "Type message and press Enter..."
	ta.Focus()
	ta.Prompt = "┃ "
	ta.CharLimit = 0
	ta.SetHeight(2)
	ta.ShowLineNumbers = false

	return model{
		vp:       vp,
		input:    ta,
		messages: []string{},
		server:   serverAddr,
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(textarea.Blink, connectCmd(m.server))
}

func connectCmd(addr string) tea.Cmd {
	return func() tea.Msg {
		conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
		if err != nil {
			return netMsg(fmt.Sprintf("[error] connect: %v", err))
		}

		return connectedMsg{conn: conn}
	}
}

func readLineCmd(conn net.Conn) tea.Cmd {
	return func() tea.Msg {
		if conn == nil {
			return disconnectedMsg{}
		}

		reader := bufio.NewReader(conn)
		line, err := reader.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				return disconnectedMsg{}
			}
			return netMsg(fmt.Sprintf("[error] read: %v", err))
		}

		return netMsg(strings.TrimRight(line, "\r\n"))
	}
}

func sendCmd(conn net.Conn, text string) tea.Cmd {
	return func() tea.Msg {
		if conn == nil {
			return netMsg("[error] not connected")
		}

		_, err := fmt.Fprintln(conn, text)
		if err != nil {
			return netMsg(fmt.Sprintf("[error] send: %v", err))
		}
		return nil
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {

	case connectedMsg:
		m.conn = msg.conn
		m.messages = append(m.messages, "[connected]")
		m.refreshViewport()

		return m, readLineCmd(m.conn)

	case disconnectedMsg:
		m.messages = append(m.messages, "[disconnected]")
		if m.conn != nil {
			_ = m.conn.Close()
			m.conn = nil
		}
		m.refreshViewport()
		return m, nil

	case netMsg:
		m.messages = append(m.messages, string(msg))
		m.refreshViewport()

		if m.conn != nil && !strings.HasPrefix(string(msg), "[error] read") {
			cmds = append(cmds, readLineCmd(m.conn))
		}

	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			if m.conn != nil {
				_ = m.conn.Close()
			}
			return m, tea.Quit
		case tea.KeyEnter:
			text := m.input.Value()
			if text != "" {
				cmds = append(cmds, sendCmd(m.conn, text))
				m.input.SetValue("")
				m.refreshViewport()
			}
			return m, tea.Batch(cmds...)
		}

	case tea.WindowSizeMsg:
		// Allocate space: viewport above, input below
		m.vp.Width = msg.Width
		m.vp.Height = msg.Height - 4
		m.input.SetWidth(msg.Width - 2)
		m.refreshViewport()
	}

	// Let textarea handle remaining keys
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	if cmd != nil {
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

func (m *model) refreshViewport() {
	contentWidth := m.vp.Width - m.vp.Style.GetHorizontalFrameSize()

	var b strings.Builder
	for _, line := range m.messages {
		wrapped := lipgloss.NewStyle().Width(contentWidth).Render(line)
		b.WriteString(wrapped)
		if !strings.HasSuffix(wrapped, "\n") {
			b.WriteString("\n")
		}
	}
	m.vp.SetContent(b.String())
	m.vp.GotoBottom()
}

func (m model) View() string {
	header := lipgloss.NewStyle().Width(m.vp.Width).Padding(0, 1).Align(lipgloss.Center).Foreground(lipgloss.Color("241")).Bold(true).Render(m.server)
	return lipgloss.JoinVertical(
		lipgloss.Left,
		header,
		m.vp.View(),
		m.input.View(),
	)
}

func main() {
	var (
		host       string
		serverOnly bool
	)

	flag.StringVar(&host, "host", "localhost:9000", "host:port to connect to or bind the server on")
	flag.BoolVar(&serverOnly, "server", false, "run only the server")
	flag.Parse()

	if serverOnly {
		if err := startTCPServer(host); err != nil {
			fmt.Println("Server error:", err)
		}
		return
	}

	time.Sleep(200 * time.Millisecond)

	p := tea.NewProgram(initialModel(host), tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fmt.Println("Error:", err)
	}
}
