package tea

import (
	"strings"
	"testing"

	bubbletea "charm.land/bubbletea/v2"
)

// Rust #61889: a preview opens web links in the browser while local links keep
// resolving against the document's directory.
func TestSidebarDocumentWebLinksOpenInBrowserLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	var opened []string
	model.onAppLinkAction = func(action AppLinkAction) bubbletea.Cmd {
		if action.Kind == AppLinkActionOpenURL {
			opened = append(opened, action.URL)
		}
		return nil
	}
	tab := openDocumentTabAt(t, model, SidebarDocumentView{
		Path: "/workspace/docs/guide.md",
		Text: "# Guide\n\nSee [the docs](https://example.com/docs) for details.\n",
	})

	rows := model.sidebarDocumentLinkRows(tab)
	if len(rows) != 1 {
		t.Fatalf("link rows = %#v, want the painted web link", rows)
	}
	if !rows[0].Web || rows[0].Path != "https://example.com/docs" {
		t.Fatalf("web row = %#v, want the OSC-8 destination", rows[0])
	}
	if rows[0].Row < 0 || rows[0].Width <= 0 {
		t.Fatalf("web row position = %#v, want a painted span", rows[0])
	}
	if index := model.sidebarDocumentLinkAtRow(tab, rows[0].Row, rows[0].Column); index != 0 {
		t.Fatalf("hit test = %d, want the web link", index)
	}

	model.cycleSidebarDocumentLink(tab, true)
	if tab.Link != 0 {
		t.Fatalf("cycled link = %d, want the web link", tab.Link)
	}
	runTeaCmd(t, model, model.followSidebarDocumentLink(tab))
	if len(opened) != 1 || opened[0] != "https://example.com/docs" {
		t.Fatalf("opened = %#v, want the web URL handed to the browser action", opened)
	}
	if tab.PathScroll != nil {
		t.Fatal("a web link opened the path view")
	}
}

// A preview with both kinds keeps local links resolving against its directory.
func TestSidebarDocumentMixedLinksLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	var requests []string
	model.onSidebarDocument = func(request SidebarDocumentRequest) SidebarDocumentView {
		requests = append(requests, request.Path)
		return SidebarDocumentView{Path: request.Path, Text: "sibling body"}
	}
	var opened []string
	model.onAppLinkAction = func(action AppLinkAction) bubbletea.Cmd {
		if action.Kind == AppLinkActionOpenURL {
			opened = append(opened, action.URL)
		}
		return nil
	}
	tab := openDocumentTabAt(t, model, SidebarDocumentView{
		Path: "/workspace/docs/guide.md",
		Text: "Read [the sibling](other.md) and [the site](https://example.com).\n",
	})

	rows := model.sidebarDocumentLinkRows(tab)
	if len(rows) != 2 {
		t.Fatalf("link rows = %#v, want one local and one web link", rows)
	}
	if rows[0].Web || !strings.HasSuffix(rows[0].Path, "other.md") {
		t.Fatalf("first row = %#v, want the local link", rows[0])
	}
	if !rows[1].Web || rows[1].Path != "https://example.com" {
		t.Fatalf("second row = %#v, want the web link", rows[1])
	}

	runTeaCmd(t, model, model.followSidebarDocumentLinkIndex(tab, 0))
	if len(requests) != 1 || len(opened) != 0 {
		t.Fatalf("local follow = requests %#v opened %#v, want the sibling document", requests, opened)
	}
	runTeaCmd(t, model, model.followSidebarDocumentLinkIndex(tab, 1))
	if len(opened) != 1 || opened[0] != "https://example.com" {
		t.Fatalf("web follow = %#v, want the site URL", opened)
	}
}
