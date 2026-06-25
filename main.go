package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
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
	newFile         bool
	newFileName     string
	newFileInput    *textinput.Model
	confirmDelete   bool
	deleteKey       string
	searchTerm      string
	loadingMore     bool
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
	Enter    key.Binding
	Back     key.Binding
	Edit     key.Binding
	Quit     key.Binding
	Reload   key.Binding
	Add      key.Binding
	Delete   key.Binding
	Search   key.Binding
	NextPage key.Binding
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
		Add: key.NewBinding(
			key.WithKeys("ctrl+a"),
			key.WithHelp("ctrl+a", "add new file"),
		),
		Delete: key.NewBinding(
			key.WithKeys("ctrl+d"),
			key.WithHelp("ctrl+d", "delete file"),
		),
		Search: key.NewBinding(
			key.WithKeys("ctrl+s"),
			key.WithHelp("ctrl+s", "search bucket with filter prefix"),
		),
		NextPage: key.NewBinding(
			key.WithKeys("n"),
			key.WithHelp("n", "load next page"),
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
			keys.Add,
			keys.Delete,
			keys.Search,
			keys.NextPage,
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

	var client *s3.Client
	if os.Getenv("LOCAL_AWS") != "" {
		client = s3.NewFromConfig(cfg, func(o *s3.Options) {
			o.BaseEndpoint = aws.String("http://localhost:4566/")
			o.UsePathStyle = true
		})
	} else {
		client = s3.NewFromConfig(cfg)
	}

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
	queryPrefix := m.currentPrefix + m.searchTerm
	input := &s3.ListObjectsV2Input{
		Bucket:            &m.bucketName,
		Prefix:            &queryPrefix,
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

		// The delimiter scopes results to one level below the query prefix; skip anything deeper.
		if strings.Contains(strings.TrimPrefix(*obj.Key, queryPrefix), "/") {
			continue
		}
		relativePath := strings.TrimPrefix(*obj.Key, m.currentPrefix)
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
	title := m.bucketName
	if m.currentPrefix != "" {
		title = fmt.Sprintf("%s/%s", m.bucketName, m.currentPrefix)
	}
	if m.searchTerm != "" {
		title += fmt.Sprintf(" [search: %s]", m.searchTerm)
	}
	m.list.Title = title
}

func (m Model) Init() tea.Cmd {
	return m.loadItems
}

type ViewFinishedMsg struct {
	filename string
	err      error
}
type EditFinishedMsg struct {
	key         string
	filename    string
	err         error
	contentType string
}

type NewFileMsg struct {
	filename string
}
type EditFileTickMsg time.Time

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// log.Println(reflect.TypeOf(msg).Name()+" msg: ", msg)
	if m.newFile && m.newFileInput != nil {
		newFileInput, cmd := m.newFileInput.Update(msg)
		m.newFileInput = &newFileInput

		switch msg := msg.(type) {
		case tea.KeyMsg:
			if msg.Type == tea.KeyEnter {
				m.newFile = false
				return m, tea.Batch(cmd, func() tea.Msg { return NewFileMsg{filename: m.newFileInput.Value()} })
			} else if msg.Type == tea.KeyCtrlC {
				m.newFile = false
				m.newFileInput = nil
				return m, nil
			}

		}
		return m, cmd
	}
	if m.confirmDelete {
		if msg, ok := msg.(tea.KeyMsg); ok {
			m.confirmDelete = false
			if msg.String() == "y" || msg.String() == "Y" {
				key := m.deleteKey
				_, err := m.client.DeleteObject(context.TODO(), &s3.DeleteObjectInput{
					Bucket: aws.String(m.bucketName),
					Key:    aws.String(key),
				})
				if err != nil {
					return m, func() tea.Msg { return err }
				}
				m.loading = true
				m.statusMsg = fmt.Sprintf("Deleted %s", key)
				m.showStatusMsg = true
				return m, m.loadItems
			}
		}
		return m, nil
	}
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}

		// While typing a filter, let the list handle all keys (including backspace),
		// except the shortcut that re-runs the listing server-side with the typed prefix.
		if m.list.FilterState() == list.Filtering {
			if key.Matches(msg, m.keys.Search) {
				m.searchTerm = m.list.FilterInput.Value()
				m.list.ResetFilter()
				m.loading = true
				m.nextPageToken = nil
				m.loadingMore = false
				m.updateTitle()
				return m, m.loadItems
			}
			break
		}

		if key.Matches(msg, m.keys.Enter) {
			if i, ok := m.list.SelectedItem().(item); ok && i.isDir {
				m.loading = true
				m.currentPrefix = i.key
				m.searchTerm = ""
				m.nextPageToken = nil
				m.loadingMore = false
				m.list.ResetFilter()
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

				metadata := fmt.Sprintf("s3://%s/%s\nContentType: %s\nMetadata: %v\nSize: %s\nLast-Modified: %s\n%s\n\n", m.bucketName, i.key, i.contentType, obj.Metadata, humanize.Bytes(uint64(i.size)), i.modified.Format("2006-01-02 15:04:05"), strings.Repeat("-", m.lastWindowSize.Width-10))

				tmpFile, err := writeToTmpFile(metadata, obj.Body, fmt.Sprintf("%s-%s", m.bucketName, strings.ReplaceAll(i.key, "/", "_")))
				if err != nil {
					return m, func() tea.Msg { return err }
				}

				cmd := tea.ExecProcess(exec.Command("less", tmpFile), func(err error) tea.Msg {
					return ViewFinishedMsg{err: err, filename: tmpFile}
				})

				return m, cmd

			}
		} else if key.Matches(msg, m.keys.Back) {
			if m.searchTerm != "" {
				m.loading = true
				m.searchTerm = ""
				m.nextPageToken = nil
				m.loadingMore = false
				m.list.ResetFilter()
				m.updateTitle()
				return m, m.loadItems
			}
			if m.currentPrefix != "" {
				m.loading = true
				m.nextPageToken = nil
				m.loadingMore = false
				m.list.ResetFilter()
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
			m.nextPageToken = nil
			m.loadingMore = false
			return m, tea.Batch(
				m.loadItems,
			)
		} else if key.Matches(msg, m.keys.NextPage) {
			if m.hasMoreItems && !m.loading && !m.loadingMore {
				m.loadingMore = true
				return m, m.loadItems
			}
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

				tmpFile, err := writeToTmpFile("", obj.Body, fmt.Sprintf("%s-%s", m.bucketName, strings.ReplaceAll(i.key, "/", "_")))
				if err != nil {
					return m, func() tea.Msg { return err }
				}

				cmd := tea.ExecProcess(exec.Command(os.Getenv("EDITOR"), tmpFile), func(err error) tea.Msg {
					return EditFinishedMsg{err: err, filename: tmpFile, key: i.key, contentType: i.contentType}
				})

				return m, cmd

			}
		} else if !m.newFile && key.Matches(msg, m.keys.Add) {
			m.newFile = true
			textInput := textinput.New()
			textInput.Prompt = "New object: " + m.currentPrefix
			if !strings.HasSuffix(m.currentPrefix, "/") {
				textInput.Prompt += "/"
			}
			textInput.Focus()
			m.newFileInput = &textInput
			return m, textinput.Blink
		} else if key.Matches(msg, m.keys.Delete) {
			if i, ok := m.list.SelectedItem().(item); ok && !i.isDir {
				m.confirmDelete = true
				m.deleteKey = i.key
				return m, nil
			}
		}
	case NewFileMsg:
		m.newFile = false
		fileKey := m.currentPrefix + m.newFileInput.Value()
		tmpFile, err := writeToTmpFile("", nil, fmt.Sprintf("%s-%s", m.bucketName, strings.ReplaceAll(fileKey, "/", "_")))
		if err != nil {
			return m, func() tea.Msg { return err }
		}

		cmd := tea.ExecProcess(exec.Command(os.Getenv("EDITOR"), tmpFile), func(err error) tea.Msg {
			return EditFinishedMsg{err: err, filename: tmpFile, key: fileKey, contentType: "text/plain"}
		})
		return m, cmd

	case ViewFinishedMsg:
		os.Remove(msg.filename)
	case EditFinishedMsg:
		if msg.err != nil {
			m.statusMsg = fmt.Sprintf("Edit cancelled, not uploaded: %v", msg.err)
			m.showStatusMsg = true
			os.Remove(msg.filename)
			return m, nil
		}
		tmp, err := os.Open(msg.filename)
		if err != nil {
			return m, func() tea.Msg { return err }
		}
		_, err = m.client.PutObject(context.TODO(), &s3.PutObjectInput{
			Bucket:      aws.String(m.bucketName),
			Key:         aws.String(msg.key),
			Body:        tmp,
			ContentType: aws.String(msg.contentType),
		})
		if err != nil {
			return m, func() tea.Msg { return err }
		}
		m.editFileStatus = fmt.Sprintf(" → Uploaded %s %s to %s/%s!", msg.filename, msg.contentType, m.bucketName, msg.key)
		err = os.Remove(msg.filename)
		if err != nil {
			return m, func() tea.Msg { return err }
		}

		return m, tea.Batch(m.loadItems, tea.Tick(time.Second*2, func(t time.Time) tea.Msg {
			return EditFileTickMsg(t)
		}))
	case EditFileTickMsg:
		m.editFileStatus = ""

	case tea.WindowSizeMsg:
		m.lastWindowSize = msg
		m.updateListSize(msg.Width, msg.Height)

	case itemsLoadedMsg:
		if m.loadingMore {
			m.currentItems = append(m.currentItems, msg.items...)
		} else {
			m.currentItems = msg.items
		}
		m.loadingMore = false
		m.hasMoreItems = msg.hasMore
		m.nextPageToken = msg.nextToken
		m.loading = false
		m.list.SetItems(m.currentItems)
		if m.lastWindowSize.Width > 0 && m.lastWindowSize.Height > 0 {
			m.updateListSize(m.lastWindowSize.Width, m.lastWindowSize.Height)
		}

		if len(m.currentItems) == 0 {
			m.statusMsg = "Directory is empty"
		} else if msg.hasMore {
			m.statusMsg = fmt.Sprintf("Showing %d items (More available - press 'n' for next page)", len(m.currentItems))
		} else {
			m.statusMsg = fmt.Sprintf("Showing %d items (End of list)", len(m.currentItems))
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
	if reader != nil {
		if _, err := io.Copy(tmpFile, reader); err != nil {
			return "", fmt.Errorf("failed to write to temp file: %w", err)
		}
	}

	// Return the path to the temporary file
	return tmpFile.Name(), nil
}

func (m *Model) updateListSize(width, height int) {
	h, v := docStyle.GetFrameSize()
	m.list.SetSize(width-h, height-v-1)

}

func (m Model) footer() string {
	if m.confirmDelete {
		return docStyle.Render(fmt.Sprintf("Delete %s? (y/N)", m.deleteKey))
	} else if m.newFile && m.newFileInput != nil {
		return m.newFileInput.View()

	} else if m.showStatusMsg {
		statusMsg := m.statusMsg
		if m.editFileStatus != "" {
			statusMsg = m.editFileStatus
		}
		return docStyle.Render(statusMsg)
	}
	return ""
}

func (m Model) View() string {
	if m.loading {
		return "Loading..."
	}

	// return m.list.View()
	return lipgloss.JoinVertical(lipgloss.Top, m.list.View(), m.footer())
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
