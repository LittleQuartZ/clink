package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"regexp"

	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// colorizeLine applies ANSI styling to usernames based on 6-hex-digit id from the server.
func colorizeLine(s string) string {
	reChat := regexp.MustCompile(`^(.+?) \(([0-9a-fA-F]{6})\):[ \t]*(.*)$`)
	if m := reChat.FindStringSubmatch(s); m != nil {
		id := strings.ToLower(m[2])
		nameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#" + id)).Bold(true)
		name := nameStyle.Render(m[1])
		rest := strings.TrimSpace(m[3])
		return fmt.Sprintf("%s: %s", name, rest)
	}

	reJoinLeave := regexp.MustCompile(`^\[(join|leave)\] (.+?) \(([0-9a-fA-F]{6})\)$`)
	if m := reJoinLeave.FindStringSubmatch(s); m != nil {
		id := strings.ToLower(m[3])
		nameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#" + id)).Bold(true)
		uname := nameStyle.Render(m[2])
		return fmt.Sprintf("[%s] %s", m[1], uname)
	}

	reRename := regexp.MustCompile(`^\[rename\] (.+?) \(([0-9a-fA-F]{6})\) -> (.+)$`)
	if m := reRename.FindStringSubmatch(s); m != nil {
		id := strings.ToLower(m[2])
		nameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#" + id)).Bold(true)
		oldN := nameStyle.Render(m[1])
		newN := nameStyle.Render(m[3])
		return fmt.Sprintf("[rename] %s -> %s", oldN, newN)
	}

	reWelcome := regexp.MustCompile(`^Welcome (.+?) \(([0-9a-fA-F]{6})\)$`)
	if m := reWelcome.FindStringSubmatch(s); m != nil {
		id := strings.ToLower(m[2])
		nameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#" + id)).Bold(true)
		uname := nameStyle.Render(m[1])
		return fmt.Sprintf("Welcome %s", uname)
	}

	return s
}

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
	if m.conn != nil {
		return tea.Batch(textarea.Blink, readLineCmd(m.conn))
	}
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
		m.messages = append(m.messages, colorizeLine(string(msg)))
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

	// Pre-connect and read initial welcome/instruction before starting UI
	var preConn net.Conn
	var preMsgs []string
	if conn, err := net.DialTimeout("tcp", host, 3*time.Second); err == nil {
		preConn = conn
		// Read up to two initial lines with a short deadline
		_ = conn.SetReadDeadline(time.Now().Add(1 * time.Second))
		r := bufio.NewReader(conn)
		for i := 0; i < 2; i++ {
			line, err := r.ReadString('\n')
			if err != nil {
				break
			}
			preMsgs = append(preMsgs, strings.TrimRight(line, "\r\n"))
		}
		_ = conn.SetReadDeadline(time.Time{})
	}

	m := initialModel(host)
	if preConn != nil {
		m.conn = preConn
		m.messages = append(m.messages, preMsgs...)
	}

	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fmt.Println("Error:", err)
	}
}
