package main

import (
	"fmt"
	"log"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mtyurt/s3n/logger"
)

var (
	titleStyle = func() lipgloss.Style {
		b := lipgloss.RoundedBorder()
		b.Right = "├"
		return lipgloss.NewStyle().BorderStyle(b).Padding(0, 1)
	}()

	infoStyle = func() lipgloss.Style {
		b := lipgloss.RoundedBorder()
		b.Left = "┤"
		return titleStyle.Copy().BorderStyle(b)
	}()

	timestampStyle = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "236", Dark: "248"})

	filterPrompt = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#04B575", Dark: "#ECFD65"})

	filterCursor = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#EE6FF8", Dark: "#EE6FF8"})

	helpStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#9B9B9B"))
)

type ViewModel struct {
	viewport      viewport.Model
	title         string
	body          string
	filterInput   textinput.Model
	filterEnabled bool
}

func NewView(title string, width, height int, body string) ViewModel {
	view := viewport.New(width-10, height)
	view.SetYOffset(1)
	filterInput := textinput.New()
	filterInput.Prompt = "Filter: "
	filterInput.Width = width - 20 - len(filterInput.Prompt)
	filterInput.Focus()

	m := ViewModel{
		viewport:    view,
		title:       title + "\t" + helpStyle.Render("Press / to filter"),
		body:        body,
		filterInput: filterInput,
	}

	m.updateContent()
	return m
}

func (m *ViewModel) updateContent() {
	content := m.body
	if m.filterEnabled {
		content = highlightOccurencesCaseInsensitive(content, m.filterInput.Value())
	}
	m.viewport.SetContent(content)
}

func highlightOccurencesCaseInsensitive(a, b string) string {
	if b == "" {
		return a
	}

	// Convert both strings to lower case for case-insensitive comparison
	lowerA := strings.ToLower(a)
	lowerB := strings.ToLower(b)

	// Find all occurrences of b in a
	var lastIndex int
	var result strings.Builder
	for {
		index := strings.Index(lowerA[lastIndex:], lowerB)
		if index == -1 {
			break
		}
		// Append the original text plus the highlighted occurrence
		result.WriteString(a[lastIndex : lastIndex+index])
		result.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("205")).Render(a[lastIndex+index : lastIndex+index+len(b)]))
		lastIndex += index + len(b)
	}
	// Append any remaining text after the last occurrence
	result.WriteString(a[lastIndex:])

	return result.String()
}

func (m ViewModel) SetSize(width, height int) {
	// m.eventsView.Width = width
	// m.eventsView.Height = height
	// m.filterInput.Width = width - 10 - len(m.filterInput.Prompt)
}
func (m ViewModel) Focused() bool {
	return !m.filterEnabled
}
func (m ViewModel) Init() tea.Cmd {
	return nil
}

func (m *ViewModel) clearFilter() {
	m.filterInput.SetValue("")
	m.updateContent()
}

func (m ViewModel) Update(msg tea.Msg) (ViewModel, tea.Cmd) {
	var cmd tea.Cmd
	cmds := []tea.Cmd{}
	log.Println("view update", msg)

	newFilter := false
	if msg, ok := msg.(tea.KeyMsg); ok {
		if !m.filterEnabled {
			switch msg.String() {
			case "/":
				m.filterEnabled = true
				m.filterInput.Focus()

				newFilter = true
			}
		} else {
			switch msg.String() {
			case "esc":
				m.filterEnabled = false
				m.clearFilter()
			}
		}
	}

	if msg, ok := msg.(tea.WindowSizeMsg); ok {
		logger.Printf("events set window size: %d, %d\n", msg.Width, msg.Height)
		m.SetSize(msg.Width, msg.Height)
		m.viewport.Width = msg.Width - 10
		// m.eventsView.Height = msg.Height
		m.filterInput.Width = msg.Width - 20 - len(m.filterInput.Prompt)
		m.updateContent()
	}

	if m.filterEnabled && !newFilter {
		newFilterInputViewModel, inputCmd := m.filterInput.Update(msg)
		m.filterInput = newFilterInputViewModel
		cmds = append(cmds, inputCmd)
		m.updateContent()
	} else {
		m.viewport, cmd = m.viewport.Update(msg)
		cmds = append(cmds, cmd)
	}
	return m, tea.Batch(cmds...)
}

func (m ViewModel) headerView() string {
	width := m.viewport.Width
	if m.filterEnabled {
		view := titleStyle.Render(m.filterInput.View())
		line := strings.Repeat("─", max(0, width-lipgloss.Width(view)-10))
		return lipgloss.JoinHorizontal(lipgloss.Center, view, line)
	}
	title := titleStyle.Render(m.title)
	line := strings.Repeat("─", max(0, width-lipgloss.Width(title)))
	return lipgloss.JoinHorizontal(lipgloss.Center, title, line)
}

func (m ViewModel) footerView() string {
	info := infoStyle.Render(fmt.Sprintf("%3.f%%", m.viewport.ScrollPercent()*100))
	line := strings.Repeat("─", max(0, m.evviewportidth-lipgloss.Width(info)))
	return lipgloss.JoinHorizontal(lipgloss.Center, line, info)
}
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func (m ViewModel) View() string {
	return lipgloss.NewStyle().Margin(5, 1).Width(m.viewport.Width + 2).Height(m.viewport.Height + 3).Render(
		fmt.Sprintf("%s\n%s\n%s\n", m.headerView(), m.viewport.View(), m.footerView()))
}
