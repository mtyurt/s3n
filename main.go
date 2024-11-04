package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/dustin/go-humanize"
	"github.com/mtyurt/s3n/logger"
)

const PAGE_SIZE = 100

var (
	helpStyleKey = lipgloss.NewStyle().Foreground(lipgloss.Color("#9B9BCC")).Bold(true)
	helpStyleVal = lipgloss.NewStyle().Foreground(lipgloss.Color("#9B9B9B"))
)

type Model struct {
	list            list.Model
	help            help.Model
	keys            keyMap
	client          *s3.Client
	bucketName      string
	currentPrefix   string
	editFileStatus  string
	loading         bool
	nextPageToken   *string
	hasMoreItems    bool
	currentItems    []list.Item
	statusMsg       string
	showStatusMsg   bool
	lastWindowSize  tea.WindowSizeMsg
	showContentType bool
}

type item struct {
	key         string // full path for navigation
	displayKey  string // relative path for display
	contentType string
	size        int64
	modified    time.Time
	isDir       bool
}

func (i item) Title() string {
	if i.displayKey == "" {
		return "" // Don't show empty items
	}
	if i.isDir {
		return "📁 " + i.displayKey
	}
	return "📄 " + i.displayKey
}

func (i item) Description() string {
	if i.key == "" {
		return "" // Don't show description for empty items
	}
	if i.isDir {
		return "Directory"
	}
	d := fmt.Sprintf("%s, Modified: %s", humanize.Bytes(uint64(i.size)), i.modified.Format("2006-01-02 15:04:05"))
	if i.contentType != "" {
		d += fmt.Sprintf(", Content-Type: %s", i.contentType)
	}
	return d
}

func (i item) FilterValue() string {
	return i.key
}

type keyMap struct {
	Enter  key.Binding
	Back   key.Binding
	Edit   key.Binding
	Quit   key.Binding
	Reload key.Binding
}

func newKeyMap() keyMap {
	return keyMap{
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
		Quit: key.NewBinding(
			key.WithKeys("q", "ctrl+c"),
			key.WithHelp("q", "quit"),
		),
		Reload: key.NewBinding(
			key.WithKeys("ctrl+r"),
			key.WithHelp("ctrl+r", "reload"),
		),
	}
}

func shortHelpKeys(keys keyMap) func() []key.Binding {
	return func() []key.Binding {
		return []key.Binding{
			keys.Enter,
			keys.Back,
			keys.Edit,
			keys.Reload,
		}

	}
}

func initialModel(bucketName string) Model {
	keys := newKeyMap()

	delegate := list.NewDefaultDelegate()
	delegate.ShowDescription = true
	delegate.SetSpacing(1)

	// Create the list with empty items initially
	l := list.New([]list.Item{}, delegate, 0, 0)
	l.SetShowTitle(true)
	l.SetShowStatusBar(true)
	l.SetStatusBarItemName("object", "objects")
	l.Title = fmt.Sprintf("%s", bucketName)
	l.SetShowHelp(true)
	l.AdditionalFullHelpKeys = shortHelpKeys(keys)

	// Optionally customize the list styles
	l.Styles.Title = lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("205")).
		Padding(1, 0)

	l.Styles.FilterPrompt = lipgloss.NewStyle().
		Foreground(lipgloss.Color("205"))

	l.Styles.FilterCursor = lipgloss.NewStyle().
		Foreground(lipgloss.Color("205"))

	cfg, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		panic(err)
	}

	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String("http://localhost:4566/")
		o.UsePathStyle = true
	})
	log.Println("s3 config initiated")

	return Model{
		list:       l,
		help:       help.New(),
		keys:       keys,
		loading:    true,
		client:     client,
		bucketName: bucketName,
	}
}

type statusMessageTimeoutMsg struct{}
type viewExit struct{}

var docStyle = lipgloss.NewStyle().Margin(1, 2)

// Add this message type at the top level
type itemsLoadedMsg struct {
	items     []list.Item
	hasMore   bool
	nextToken *string
}

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
		if prefix.Prefix != nil && *prefix.Prefix != "" && *prefix.Prefix != m.currentPrefix {
			relativePath := strings.TrimPrefix(*prefix.Prefix, m.currentPrefix)
			// Remove trailing slash from display
			relativePath = strings.TrimSuffix(relativePath, "/")

			items = append(items, item{
				key:        *prefix.Prefix, // Keep full path for navigation
				displayKey: relativePath,   // Use relative path for display
				isDir:      true,
			})
		}
	}

	// Process files
	for _, obj := range output.Contents {
		if obj.Key == nil || *obj.Key == "" || *obj.Key == m.currentPrefix {
			continue
		}

		relativePath := strings.TrimPrefix(*obj.Key, m.currentPrefix)
		if strings.Contains(relativePath, "/") {
			continue
		}
		// Get content type using HeadObject
		headInput := &s3.HeadObjectInput{
			Bucket: &m.bucketName,
			Key:    obj.Key,
		}

		contentType := ""
		if m.showContentType {
			if headOutput, err := m.client.HeadObject(context.TODO(), headInput); err == nil && headOutput.ContentType != nil {
				contentType = *headOutput.ContentType
			}
		}
		items = append(items, item{
			key:         *obj.Key, // Keep the full path for consistency
			size:        *obj.Size,
			contentType: contentType,
			displayKey:  relativePath,
			modified:    *obj.LastModified,
			isDir:       false,
		})
	}

	return itemsLoadedMsg{
		items:     items,
		hasMore:   *output.IsTruncated,
		nextToken: output.NextContinuationToken,
	}
}

func (m *Model) updateTitle() {
	if m.currentPrefix == "" {
		m.list.Title = fmt.Sprintf("%s", m.bucketName)
	} else {
		m.list.Title = fmt.Sprintf("%s/%s", m.bucketName, m.currentPrefix)
	}
}

func (m Model) Init() tea.Cmd {
	return m.loadItems
}

type ViewFinishedMsg struct {
	filename string
	err      error
}
type EditFinishedMsg struct {
	key      string
	filename string
	err      error
}
type EditFileTickMsg time.Time

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	log.Println(reflect.TypeOf(msg).Name()+" msg: ", msg)
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		if key.Matches(msg, m.keys.Enter) {
			if i, ok := m.list.SelectedItem().(item); ok && i.isDir {
				m.loading = true
				m.currentPrefix = i.key
				m.updateTitle()
				return m, tea.Batch(
					m.loadItems,
				)
			} else if !i.isDir {

				obj, err := m.client.GetObject(context.TODO(), &s3.GetObjectInput{
					Bucket: aws.String(m.bucketName),
					Key:    aws.String(i.key),
				})
				if err != nil {
					return m, func() tea.Msg { return err }
				}

				defer obj.Body.Close()

				metadata := fmt.Sprintf("s3://%s/%s\nMetadata: %v\nSize: %s\nLast-Modified: %s\n%s\n\n", m.bucketName, i.key, obj.Metadata, humanize.Bytes(uint64(i.size)), i.modified.Format("2006-01-02 15:04:05"), strings.Repeat("-", m.lastWindowSize.Width-10))

				tmpFile, err := writeToTmpFile(metadata, obj.Body, fmt.Sprintf("%s-%s", m.bucketName, i.key))
				if err != nil {
					return m, func() tea.Msg { return err }
				}

				cmd := tea.ExecProcess(exec.Command("less", tmpFile), func(err error) tea.Msg {
					return ViewFinishedMsg{err: err, filename: tmpFile}
				})

				return m, cmd

			}
		} else if key.Matches(msg, m.keys.Back) {
			if m.currentPrefix != "" {
				m.loading = true
				// Remove the last directory from the prefix
				parts := strings.Split(strings.TrimSuffix(m.currentPrefix, "/"), "/")
				if len(parts) > 0 {
					parts = parts[:len(parts)-1]
					m.currentPrefix = strings.Join(parts, "/")
					if m.currentPrefix != "" {
						m.currentPrefix += "/"
					}
				} else {
					m.currentPrefix = ""
				}
				m.updateTitle()
				return m, tea.Batch(
					m.loadItems,
				)
			}
		} else if key.Matches(msg, m.keys.Reload) {
			m.loading = true
			return m, tea.Batch(
				m.loadItems,
			)
		} else if key.Matches(msg, m.keys.Edit) {

			if i, ok := m.list.SelectedItem().(item); ok {

				if i.isDir {
					// m.statusMsg = "Cannot edit a directory"
					return m, nil
				}

				obj, err := m.client.GetObject(context.TODO(), &s3.GetObjectInput{
					Bucket: aws.String(m.bucketName),
					Key:    aws.String(i.key),
				})
				if err != nil {
					return m, func() tea.Msg { return err }
				}

				defer obj.Body.Close()

				tmpFile, err := writeToTmpFile("", obj.Body, fmt.Sprintf("%s-%s", m.bucketName, i.key))
				if err != nil {
					return m, func() tea.Msg { return err }
				}

				cmd := tea.ExecProcess(exec.Command(os.Getenv("EDITOR"), tmpFile), func(err error) tea.Msg {
					return EditFinishedMsg{err: err, filename: tmpFile, key: i.key}
				})

				return m, cmd

			}
		}
	case ViewFinishedMsg:
		os.Remove(msg.filename)
	case EditFinishedMsg:
		tmp, err := os.Open(msg.filename)
		if err != nil {
			return m, func() tea.Msg { return err }
		}
		_, err = m.client.PutObject(context.TODO(), &s3.PutObjectInput{
			Bucket: aws.String(m.bucketName),
			Key:    aws.String(msg.key),
			Body:   tmp,
		})
		if err != nil {
			return m, func() tea.Msg { return err }
		}
		m.editFileStatus = fmt.Sprintf(" → Uploaded %s to %s/%s!", msg.filename, m.bucketName, msg.key)
		err = os.Remove(msg.filename)
		if err != nil {
			return m, func() tea.Msg { return err }
		}

		return m, tea.Tick(time.Second*2, func(t time.Time) tea.Msg {
			return EditFileTickMsg(t)
		})
	case EditFileTickMsg:
		m.editFileStatus = ""

	case tea.WindowSizeMsg:
		m.lastWindowSize = msg
		m.updateListSize(msg.Width, msg.Height)

	case itemsLoadedMsg:
		m.currentItems = msg.items
		m.hasMoreItems = msg.hasMore
		m.nextPageToken = msg.nextToken
		m.loading = false
		m.list.SetItems(msg.items)
		if m.lastWindowSize.Width > 0 && m.lastWindowSize.Height > 0 {
			m.updateListSize(m.lastWindowSize.Width, m.lastWindowSize.Height)
		}

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
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func writeToTmpFile(metadata string, reader io.Reader, fileName string) (string, error) {
	tmpFilePath := fmt.Sprintf("/tmp/%s", fileName)
	tmpFile, err := os.Create(tmpFilePath)
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	defer tmpFile.Close()

	// Copy the contents of the reader to the temporary file
	if metadata != "" {
		if _, err := io.WriteString(tmpFile, metadata); err != nil {
			return "", fmt.Errorf("failed to write metadata to temp file: %w", err)
		}
	}
	if _, err := io.Copy(tmpFile, reader); err != nil {
		return "", fmt.Errorf("failed to write to temp file: %w", err)
	}

	// Return the path to the temporary file
	return tmpFile.Name(), nil
}

func (m *Model) updateListSize(width, height int) {
	h, v := docStyle.GetFrameSize()
	m.list.SetSize(width-h, height-v-1)

}

func (m Model) View() string {
	if m.loading {
		return "Loading..."
	}

	statusMsg := ""
	if m.showStatusMsg {
		statusMsg = m.statusMsg
		if m.editFileStatus != "" {
			statusMsg = m.editFileStatus
		}
	}
	// return m.list.View()
	return lipgloss.JoinVertical(lipgloss.Top, m.list.View(), docStyle.Render(statusMsg))
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
