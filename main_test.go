// ABOUTME: Tests for the TUI Update logic in main.go.
// ABOUTME: Covers edit-cancel handling and filter-mode key behavior.
package main

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
)

func TestEditFinishedWithErrorDoesNotUpload(t *testing.T) {
	f, err := os.CreateTemp("", "s3n-test-*")
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("content")
	f.Close()

	// nil client: any attempt to upload would panic, proving the upload was skipped.
	m := Model{}
	updated, _ := m.Update(EditFinishedMsg{
		err:         errors.New("exit status 1"),
		filename:    f.Name(),
		key:         "foo",
		contentType: "text/plain",
	})
	um := updated.(Model)

	if !um.showStatusMsg || um.statusMsg == "" {
		t.Errorf("expected a status message indicating the upload was skipped")
	}
	if _, err := os.Stat(f.Name()); !os.IsNotExist(err) {
		t.Errorf("expected temp file %q to be removed", f.Name())
	}
}

func TestBackspaceWhileFilteringDoesNotNavigate(t *testing.T) {
	m := initialModel("test-bucket")
	m.currentPrefix = "a/b/"
	m.list.SetItems([]list.Item{item{key: "a/b/x", displayKey: "x"}})

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = updated.(Model)
	if m.list.FilterState() != list.Filtering {
		t.Fatalf("expected list to be in Filtering state, got %v", m.list.FilterState())
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m = updated.(Model)

	if m.currentPrefix != "a/b/" {
		t.Errorf("backspace while filtering navigated away: currentPrefix = %q, want %q", m.currentPrefix, "a/b/")
	}
}

func TestEnterDirectoryResetsAppliedFilter(t *testing.T) {
	m := initialModel("test-bucket")
	m.loading = false
	m.list.SetItems([]list.Item{item{key: "sub/", displayKey: "sub", isDir: true}})

	// Type a filter and apply it with Enter.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.list.FilterState() != list.FilterApplied {
		t.Fatalf("expected FilterApplied after applying filter, got %v", m.list.FilterState())
	}

	// Enter on the directory navigates in and should clear the filter.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	if m.currentPrefix != "sub/" {
		t.Errorf("expected navigation into directory, currentPrefix = %q", m.currentPrefix)
	}
	if m.list.FilterState() != list.Unfiltered {
		t.Errorf("expected filter to be reset after navigating, got %v", m.list.FilterState())
	}
}

func TestCtrlDOnFilePromptsConfirmation(t *testing.T) {
	m := initialModel("test-bucket")
	m.loading = false
	m.list.SetItems([]list.Item{item{key: "a/b/file.txt", displayKey: "file.txt"}})

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	m = updated.(Model)

	if !m.confirmDelete {
		t.Errorf("expected confirmDelete to be set after ctrl+d on a file")
	}
	if m.deleteKey != "a/b/file.txt" {
		t.Errorf("expected deleteKey = %q, got %q", "a/b/file.txt", m.deleteKey)
	}
}

func TestCtrlDOnDirectoryDoesNothing(t *testing.T) {
	m := initialModel("test-bucket")
	m.loading = false
	m.list.SetItems([]list.Item{item{key: "sub/", displayKey: "sub", isDir: true}})

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	m = updated.(Model)

	if m.confirmDelete {
		t.Errorf("expected ctrl+d on a directory to be ignored")
	}
}

func TestDeleteCancelledDoesNotDelete(t *testing.T) {
	// nil client: an actual delete would panic, proving cancel skipped it.
	m := Model{confirmDelete: true, deleteKey: "a/b/file.txt"}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m = updated.(Model)

	if m.confirmDelete {
		t.Errorf("expected confirmDelete to be cleared after cancelling")
	}
}

func TestSearchShortcutWhileFilteringSetsSearchTerm(t *testing.T) {
	m := initialModel("test-bucket")
	m.loading = false
	m.currentPrefix = "logs/"
	m.list.SetItems([]list.Item{item{key: "logs/a.txt", displayKey: "a.txt"}})

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = updated.(Model)
	for _, r := range "foo" {
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = updated.(Model)
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = updated.(Model)

	if m.searchTerm != "foo" {
		t.Errorf("expected searchTerm = %q, got %q", "foo", m.searchTerm)
	}
	if m.list.FilterState() != list.Unfiltered {
		t.Errorf("expected local filter to be reset after searching, got %v", m.list.FilterState())
	}
}

func TestBackExitsSearchWithoutNavigatingUp(t *testing.T) {
	m := initialModel("test-bucket")
	m.loading = false
	m.currentPrefix = "logs/"
	m.searchTerm = "foo"
	m.list.SetItems([]list.Item{item{key: "logs/foo.txt", displayKey: "foo.txt"}})

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m = updated.(Model)

	if m.searchTerm != "" {
		t.Errorf("expected search to be cleared, got %q", m.searchTerm)
	}
	if m.currentPrefix != "logs/" {
		t.Errorf("expected to stay in same prefix, got %q", m.currentPrefix)
	}
}

func TestNextPageRequestsMoreWhenAvailable(t *testing.T) {
	m := initialModel("test-bucket")
	m.loading = false
	m.hasMoreItems = true
	m.list.SetItems([]list.Item{item{key: "a.txt", displayKey: "a.txt"}})

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m = updated.(Model)

	if !m.loadingMore {
		t.Errorf("expected loadingMore to be set after pressing n with more items available")
	}
}

func TestNextPageIgnoredWhenNoMore(t *testing.T) {
	m := initialModel("test-bucket")
	m.loading = false
	m.hasMoreItems = false
	m.list.SetItems([]list.Item{item{key: "a.txt", displayKey: "a.txt"}})

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m = updated.(Model)

	if m.loadingMore {
		t.Errorf("expected loadingMore to stay false when no more items")
	}
}

func TestItemsLoadedAppendsWhenLoadingMore(t *testing.T) {
	m := initialModel("test-bucket")
	m.currentItems = []list.Item{item{key: "a.txt", displayKey: "a.txt"}}
	m.loadingMore = true

	updated, _ := m.Update(itemsLoadedMsg{
		items:   []list.Item{item{key: "b.txt", displayKey: "b.txt"}},
		hasMore: false,
	})
	m = updated.(Model)

	if len(m.currentItems) != 2 {
		t.Errorf("expected 2 items after appending page, got %d", len(m.currentItems))
	}
	if m.loadingMore {
		t.Errorf("expected loadingMore to be reset after load")
	}
}

func TestErrorReplacesScreen(t *testing.T) {
	m := initialModel("test-bucket")
	// Init sets loading = true; an initial load failure must surface, not hang on "Loading...".
	if !m.loading {
		t.Fatalf("expected model to start in loading state")
	}

	updated, _ := m.Update(errors.New("AccessDenied: token expired"))
	m = updated.(Model)

	if m.loading {
		t.Errorf("expected loading to be cleared after an error")
	}
	if m.errMsg == "" {
		t.Errorf("expected an error message to be recorded")
	}

	view := m.View()
	if !strings.Contains(view, "AccessDenied: token expired") {
		t.Errorf("expected the error to take over the screen, got %q", view)
	}
}

func TestSuccessfulLoadClearsError(t *testing.T) {
	m := initialModel("test-bucket")
	m.errMsg = "AccessDenied: token expired"

	updated, _ := m.Update(itemsLoadedMsg{
		items:   []list.Item{item{key: "a.txt", displayKey: "a.txt"}},
		hasMore: false,
	})
	m = updated.(Model)

	if m.errMsg != "" {
		t.Errorf("expected error to be cleared after a successful load, got %q", m.errMsg)
	}
}

func TestItemsLoadedReplacesWhenNotLoadingMore(t *testing.T) {
	m := initialModel("test-bucket")
	m.currentItems = []list.Item{
		item{key: "a.txt", displayKey: "a.txt"},
		item{key: "b.txt", displayKey: "b.txt"},
	}
	m.loadingMore = false

	updated, _ := m.Update(itemsLoadedMsg{
		items:   []list.Item{item{key: "c.txt", displayKey: "c.txt"}},
		hasMore: false,
	})
	m = updated.(Model)

	if len(m.currentItems) != 1 {
		t.Errorf("expected items to be replaced (1 item), got %d", len(m.currentItems))
	}
}
