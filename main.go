package main

import (
	"fmt"
	"strings"

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
}

func initialModel() model {
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
	}
}

func (m model) Init() tea.Cmd {
	return textarea.Blink
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			return m, tea.Quit
		case tea.KeyEnter:
			text := m.input.Value()
			if text != "" {
				m.messages = append(m.messages, fmt.Sprintf("You: %s", text))
				m.messages = append(m.messages, fmt.Sprintf("Bot: %s", text))
				m.input.SetValue("")
				m.refreshViewport()
			}
			return m, nil
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
	cmds = append(cmds, cmd)

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
	return lipgloss.JoinVertical(
		lipgloss.Left,
		m.vp.View(),
		m.input.View(),
	)
}

func main() {
	p := tea.NewProgram(initialModel(), tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fmt.Println("Error:", err)
	}
}
