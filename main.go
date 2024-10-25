package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mtyurt/s3n/logger"
)

const PAGE_SIZE = 100

type Model struct {
	list           list.Model
	help           help.Model
	keys           keyMap
	client         *s3.Client
	bucketName     string
	currentPrefix  string
	viewport       viewport.Model
	viewingFile    bool
	editingFile    bool
	loading        bool
	nextPageToken  *string
	hasMoreItems   bool
	currentItems   []list.Item
	statusMsg      string
	showStatusMsg  bool
	statusMsgTimer int
}

type item struct {
	key      string
	size     int64
	modified time.Time
	isDir    bool
}

func (i item) Title() string {
	if i.key == "" {
		return "" // Don't show empty items
	}
	if i.isDir {
		return "📁 " + i.key
	}
	return "📄 " + i.key
}

func (i item) Description() string {
	if i.key == "" {
		return "" // Don't show description for empty items
	}
	if i.isDir {
		return "Directory"
	}
	return fmt.Sprintf("Size: %d bytes, Modified: %s", i.size, i.modified.Format("2006-01-02 15:04:05"))
}

func (i item) FilterValue() string {
	return i.key
}

type keyMap struct {
	Up        key.Binding
	Down      key.Binding
	Enter     key.Binding
	Back      key.Binding
	Edit      key.Binding
	View      key.Binding
	Quit      key.Binding
	Help      key.Binding
	Reload    key.Binding
	NextPage  key.Binding
	PrevPage  key.Binding
	FirstPage key.Binding
	LastPage  key.Binding
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.Enter, k.Back, k.Edit, k.View, k.NextPage, k.PrevPage}
}

func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.Enter},
		{k.Back, k.Edit, k.View},
		{k.NextPage, k.PrevPage, k.FirstPage},
		{k.LastPage, k.Reload, k.Help},
		{k.Quit},
	}
}

func newKeyMap() keyMap {
	return keyMap{
		Up: key.NewBinding(
			key.WithKeys("up", "k"),
			key.WithHelp("↑/k", "up"),
		),
		Down: key.NewBinding(
			key.WithKeys("down", "j"),
			key.WithHelp("↓/j", "down"),
		),
		Enter: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "enter dir/view file"),
		),
		Back: key.NewBinding(
			key.WithKeys("backspace", "h"),
			key.WithHelp("backspace/h", "go back"),
		),
		Edit: key.NewBinding(
			key.WithKeys("ctrl+e"),
			key.WithHelp("ctrl+e", "edit file"),
		),
		View: key.NewBinding(
			key.WithKeys("ctrl+v"),
			key.WithHelp("ctrl+v", "view file"),
		),
		Quit: key.NewBinding(
			key.WithKeys("q", "ctrl+c"),
			key.WithHelp("q", "quit"),
		),
		Help: key.NewBinding(
			key.WithKeys("?"),
			key.WithHelp("?", "toggle help"),
		),
		Reload: key.NewBinding(
			key.WithKeys("ctrl+r"),
			key.WithHelp("ctrl+r", "reload"),
		),
		NextPage: key.NewBinding(
			key.WithKeys("n", "pgdown"),
			key.WithHelp("n/pgdown", "next page"),
		),
		PrevPage: key.NewBinding(
			key.WithKeys("p", "pgup"),
			key.WithHelp("p/pgup", "previous page"),
		),
		FirstPage: key.NewBinding(
			key.WithKeys("home"),
			key.WithHelp("home", "first page"),
		),
		LastPage: key.NewBinding(
			key.WithKeys("end"),
			key.WithHelp("end", "last page"),
		),
	}
}

func initialModel(bucketName string) Model {
	keys := newKeyMap()
	// Create a custom delegate
	delegate := list.NewDefaultDelegate()

	// Only show items that have a non-empty key
	delegate.ShowDescription = true

	// Create the list with empty items initially
	l := list.New([]list.Item{}, delegate, 0, 0)
	l.Title = fmt.Sprintf("S3 Bucket Explorer - %s", bucketName)
	l.SetShowHelp(false)

	// Optionally customize the list styles
	l.Styles.Title = lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("205"))

	l.Styles.FilterPrompt = lipgloss.NewStyle().
		Foreground(lipgloss.Color("205"))

	l.Styles.FilterCursor = lipgloss.NewStyle().
		Foreground(lipgloss.Color("205"))
	l.SetShowHelp(false)

	cfg, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		panic(err)
	}

	client := s3.NewFromConfig(cfg)

	return Model{
		list:       l,
		help:       help.New(),
		keys:       keys,
		loading:    true,
		client:     client,
		bucketName: bucketName,
		// viewport:   viewport.New(0, 0),
	}
}

type statusMessageTimeoutMsg struct{}

var docStyle = lipgloss.NewStyle().Margin(1, 2)

// Add this message type at the top level
type itemsLoadedMsg struct {
	items     []list.Item
	hasMore   bool
	nextToken *string
}

// Modified to return a proper tea.Cmd
func (m Model) loadItems() tea.Msg {
	input := &s3.ListObjectsV2Input{
		Bucket:            &m.bucketName,
		Prefix:            &m.currentPrefix,
		MaxKeys:           aws.Int32(PAGE_SIZE),
		ContinuationToken: m.nextPageToken,
		Delimiter:         aws.String("/"),
	}

	output, err := m.client.ListObjectsV2(context.TODO(), input)
	if err != nil {
		return err
	}

	var items []list.Item

	// Process common prefixes (directories)
	for _, prefix := range output.CommonPrefixes {
		if prefix.Prefix != nil && *prefix.Prefix != "" {
			items = append(items, item{
				key:   *prefix.Prefix,
				isDir: true,
			})
		}
	}
	// Process files
	for _, obj := range output.Contents {
		if obj.Key == nil || *obj.Key == "" || *obj.Key == m.currentPrefix {
			continue
		}

		if strings.Contains(strings.TrimPrefix(*obj.Key, m.currentPrefix), "/") {
			continue
		}

		items = append(items, item{
			key:      *obj.Key,
			size:     *obj.Size,
			modified: *obj.LastModified,
			isDir:    false,
		})
	}

	return itemsLoadedMsg{
		items:     items,
		hasMore:   *output.IsTruncated,
		nextToken: output.NextContinuationToken,
	}
}

func (m Model) Init() tea.Cmd {
	return m.loadItems
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case itemsLoadedMsg:
		m.currentItems = msg.items
		m.hasMoreItems = msg.hasMore
		m.nextPageToken = msg.nextToken
		m.loading = false
		m.list.SetItems(msg.items)
		m.list.SetHeight(len(msg.items))

		if len(msg.items) == 0 {
			m.statusMsg = "Directory is empty"
		} else if msg.hasMore {
			m.statusMsg = fmt.Sprintf("Showing %d items (More available - press 'n' for next page)", len(msg.items))
		} else {
			m.statusMsg = fmt.Sprintf("Showing %d items (End of list)", len(msg.items))
		}
		m.showStatusMsg = true

	case error:
		m.statusMsg = fmt.Sprintf("Error: %v", msg)
		m.showStatusMsg = true

	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}

	case tea.WindowSizeMsg:
		h, v := docStyle.GetFrameSize()
		m.list.SetSize(msg.Width-h, msg.Height-v)
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m Model) View() string {
	if m.loading {
		return "Loading..."
	}

	view := m.list.View()
	if m.showStatusMsg {
		view = fmt.Sprintf("%s\n\n%s", view, m.statusMsg)
	}

	return docStyle.Render(view)
}
func (m *Model) showStatusMessage(msg string) {
	m.statusMsg = msg
	m.showStatusMsg = true
	m.statusMsgTimer = 2 // Display for 2 updates
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Please provide a bucket name")
		os.Exit(1)
	}
	if os.Getenv("DEBUG") == "true" {
		f, _ := tea.LogToFile("log.txt", "debug")
		logger.Initialize(f)
		defer f.Close()
	}

	bucketName := os.Args[1]
	m := initialModel(bucketName)
	p := tea.NewProgram(m, tea.WithAltScreen())

	if _, err := p.Run(); err != nil {
		fmt.Printf("Error: %v", err)
		os.Exit(1)
	}
}
