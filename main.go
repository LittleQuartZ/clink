package main

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type netMsg string

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
	ta.Placeholder = "Type message and press Enter…"
	ta.Focus()
	ta.Prompt = "┃ "
	ta.CharLimit = 0
	ta.SetHeight(3)
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

		go func(conn net.Conn, ch chan<- tea.Msg) {
			scanner := bufio.NewScanner(conn)
			scanner.Buffer(make([]byte, 0, 1024), 64*1024)
			for scanner.Scan() {
				ch <- netMsg(scanner.Text())
			}
			if err := scanner.Err(); err != nil {
				ch <- netMsg(fmt.Sprintf("[error] read: %v", err))
			}
			ch <- netMsg("[disconnected]")
		}(conn, make(chan tea.Msg, 1))

		return netMsg("[connected]")
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
	case netMsg:
		s := string(msg)

		if s == "[connected]" {
			m.messages = append(m.messages, s)
			m.refreshViewport()
			return m, nil
		}

		m.messages = append(m.messages, s)
		m.refreshViewport()

		return m, nil
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
	header := lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Bold(true).Render(m.server)
	return lipgloss.JoinVertical(
		lipgloss.Left,
		header,
		m.vp.View(),
		m.input.View(),
	)
}

func main() {
	go func() {
		if err := startTCPServer("localhost:9000"); err != nil {
			fmt.Println("Server error:", err)
		}
	}()

	time.Sleep(200 * time.Millisecond)

	p := tea.NewProgram(initialModel("127.0.0.1:9000"), tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fmt.Println("Error:", err)
	}
}
