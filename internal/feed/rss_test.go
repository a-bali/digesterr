package feed

import (
	"strings"
	"testing"
	"time"
)

const testRSS = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:atom="http://www.w3.org/2005/Atom" xmlns:dc="http://purl.org/dc/elements/1.1/">
  <channel>
    <title>Test Feed</title>
    <link>https://example.com</link>
    <description>A test feed</description>
    <item>
      <title>Item 1</title>
      <link>https://example.com/1</link>
      <guid isPermaLink="false">abc-123</guid>
      <description>Description of item 1</description>
      <author>author@example.com</author>
      <comments>https://example.com/1#comments</comments>
      <pubDate>Mon, 01 Jan 2024 12:00:00 +0000</pubDate>
    </item>
    <item>
      <title>Item 2</title>
      <link>https://example.com/2</link>
      <guid>https://example.com/2</guid>
      <description>Description of item 2</description>
      <pubDate>Tue, 02 Jan 2024 12:00:00 +0000</pubDate>
    </item>
  </channel>
</rss>`

const testAtom = `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>Atom Test Feed</title>
  <id>urn:uuid:abc-123</id>
  <link href="https://example.com" rel="alternate"/>
  <updated>2024-01-01T12:00:00Z</updated>
  <entry>
    <title>Atom Item 1</title>
    <id>urn:uuid:item-1</id>
    <link href="https://example.com/atom/1" rel="alternate"/>
    <link href="https://example.com/atom/1#comments" rel="comments"/>
    <summary>Atom summary</summary>
    <author><name>John Doe</name></author>
    <published>2024-01-01T10:00:00Z</published>
  </entry>
</feed>`

const testRSSNoGUID = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>No GUID Feed</title>
    <link>https://example.com</link>
    <description>Feed without GUIDs</description>
    <item>
      <title>Item without GUID</title>
      <link>https://example.com/no-guid</link>
      <description>This item has no GUID</description>
      <pubDate>Wed, 03 Jan 2024 12:00:00 +0000</pubDate>
    </item>
  </channel>
</rss>`

func TestParseRSS(t *testing.T) {
	f := &RSSFetcher{}
	items, err := f.parse([]byte(testRSS))
	if err != nil {
		t.Fatalf("parseRSS failed: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}

	if items[0].Title != "Item 1" {
		t.Fatalf("expected title 'Item 1', got %s", items[0].Title)
	}
	if items[0].URL != "https://example.com/1" {
		t.Fatalf("expected url 'https://example.com/1', got %s", items[0].URL)
	}
	if items[0].GUID != "abc-123" {
		t.Fatalf("expected guid 'abc-123', got %s", items[0].GUID)
	}
	if items[0].CommentURL != "https://example.com/1#comments" {
		t.Fatalf("expected comment_url 'https://example.com/1#comments', got %s", items[0].CommentURL)
	}
	if items[0].Content != "Description of item 1" {
		t.Fatalf("expected content 'Description of item 1', got %s", items[0].Content)
	}
	if items[0].Author != "author@example.com" {
		t.Fatalf("expected author 'author@example.com', got %s", items[0].Author)
	}
	if items[0].PublishedAt.IsZero() {
		t.Fatal("expected non-zero published_at")
	}
	expected := time.Date(2024, 1, 1, 12, 0, 0, 0, time.FixedZone("", 0))
	if !items[0].PublishedAt.Equal(expected) {
		t.Fatalf("expected published_at %v, got %v", expected, items[0].PublishedAt)
	}

	if items[1].Title != "Item 2" {
		t.Fatalf("expected title 'Item 2', got %s", items[1].Title)
	}
	if items[1].GUID != "https://example.com/2" {
		t.Fatalf("expected guid 'https://example.com/2', got %s", items[1].GUID)
	}
	if items[1].CommentURL != "" {
		t.Fatalf("expected empty comment_url, got %s", items[1].CommentURL)
	}
}

func TestParseAtom(t *testing.T) {
	f := &RSSFetcher{}
	items, err := f.parse([]byte(testAtom))
	if err != nil {
		t.Fatalf("parseAtom failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}

	if items[0].Title != "Atom Item 1" {
		t.Fatalf("expected title 'Atom Item 1', got %s", items[0].Title)
	}
	if items[0].URL != "https://example.com/atom/1" {
		t.Fatalf("expected url 'https://example.com/atom/1', got %s", items[0].URL)
	}
	if items[0].GUID != "urn:uuid:item-1" {
		t.Fatalf("expected guid 'urn:uuid:item-1', got %s", items[0].GUID)
	}
	if items[0].CommentURL != "https://example.com/atom/1#comments" {
		t.Fatalf("expected comment_url 'https://example.com/atom/1#comments', got %s", items[0].CommentURL)
	}
	if items[0].Content != "Atom summary" {
		t.Fatalf("expected content 'Atom summary', got %s", items[0].Content)
	}
	if items[0].Author != "John Doe" {
		t.Fatalf("expected author 'John Doe', got %s", items[0].Author)
	}
}

func TestParseRSSNoGUID(t *testing.T) {
	f := &RSSFetcher{}
	items, err := f.parse([]byte(testRSSNoGUID))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	// Should fall back to URL when no GUID
	if items[0].GUID != items[0].URL {
		t.Fatalf("expected guid to fall back to url, got %s", items[0].GUID)
	}
}

func TestParseInvalidXML(t *testing.T) {
	f := &RSSFetcher{}
	_, err := f.parse([]byte("not xml"))
	if err == nil {
		t.Fatal("expected error for invalid XML")
	}
}

func TestDetectFeedFormat(t *testing.T) {
	tests := []struct {
		name     string
		data     string
		expected string
	}{
		{"RSS", `<rss version="2.0"><channel><item></item></channel></rss>`, "rss"},
		{"Atom", `<feed xmlns="http://www.w3.org/2005/Atom"><entry></entry></feed>`, "atom"},
		{"unknown", `<!DOCTYPE html><html></html>`, "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectFeedFormat([]byte(tt.data))
			if got != tt.expected {
				t.Fatalf("expected %s, got %s", tt.expected, got)
			}
		})
	}
}

func TestParseDate(t *testing.T) {
	tests := []struct {
		input    string
		wantOK   bool
		wantYear int
	}{
		{"Mon, 01 Jan 2024 12:00:00 +0000", true, 2024},
		{"2024-01-01T12:00:00Z", true, 2024},
		{"2024-01-01T12:00:00+00:00", true, 2024},
		{"01 Jan 2024 12:00:00 +0000", true, 2024},
		{"", false, 0},
		{"not a date", false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := parseDate(tt.input)
			if tt.wantOK && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if !tt.wantOK && err == nil {
				t.Fatal("expected error, got none")
			}
			if tt.wantOK && got.Year() != tt.wantYear {
				t.Fatalf("expected year %d, got %d", tt.wantYear, got.Year())
			}
		})
	}
}

func TestRSSContentWithEncoded(t *testing.T) {
	xml := `<?xml version="1.0"?>
<rss version="2.0" xmlns:content="http://purl.org/rss/1.0/modules/content/">
  <channel>
    <title>WP Feed</title>
    <link>https://example.com</link>
    <description>desc</description>
    <item>
      <title>WP Item</title>
      <link>https://example.com/wp</link>
      <guid>wp-1</guid>
      <description>Excerpt</description>
      <content:encoded><![CDATA[<p>Full content</p>]]></content:encoded>
      <pubDate>Mon, 01 Jan 2024 12:00:00 +0000</pubDate>
    </item>
  </channel>
</rss>`

	f := &RSSFetcher{}
	items, err := f.parse([]byte(xml))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if !strings.Contains(items[0].Content, "Full content") {
		t.Fatalf("expected content to contain 'Full content', got %s", items[0].Content)
	}
}

func TestRSSTitleStripsHTML(t *testing.T) {
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>HTML Title Feed</title>
    <link>https://example.com</link>
    <description>desc</description>
    <item>
      <title><![CDATA[<b>Bold</b> title with <a href="https://example.com">link</a>]]></title>
      <link>https://example.com/html1</link>
      <guid>html-1</guid>
      <pubDate>Mon, 01 Jan 2024 12:00:00 +0000</pubDate>
    </item>
  </channel>
</rss>`

	f := &RSSFetcher{}
	items, err := f.parse([]byte(xml))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Title != "Bold title with link" {
		t.Fatalf("expected title 'Bold title with link', got %q", items[0].Title)
	}
}

func TestRSSTitleWithHTMLEntities(t *testing.T) {
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>Entity Feed</title>
    <link>https://example.com</link>
    <description>desc</description>
    <item>
      <title>AT&amp;T announces &lt;new&gt; product</title>
      <link>https://example.com/ent1</link>
      <guid>ent-1</guid>
      <pubDate>Mon, 01 Jan 2024 12:00:00 +0000</pubDate>
    </item>
  </channel>
</rss>`

	f := &RSSFetcher{}
	items, err := f.parse([]byte(xml))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Title != "AT&T announces  product" {
		t.Fatalf("expected decoded title, got %q", items[0].Title)
	}
}

func TestAtomTitleStripsHTML(t *testing.T) {
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>Atom HTML Feed</title>
  <id>urn:uuid:atom-html</id>
  <link href="https://example.com" rel="alternate"/>
  <updated>2024-01-01T00:00:00Z</updated>
  <entry>
    <title type="html">&lt;i&gt;Italic&lt;/i&gt; title</title>
    <id>urn:uuid:atom-html-1</id>
    <link href="https://example.com/atom-html/1" rel="alternate"/>
    <summary>Summary</summary>
    <published>2024-01-01T00:00:00Z</published>
  </entry>
</feed>`

	f := &RSSFetcher{}
	items, err := f.parse([]byte(xml))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Title != "Italic title" {
		t.Fatalf("expected title 'Italic title', got %q", items[0].Title)
	}
}

func TestRSSGUIDValue(t *testing.T) {
	xml := `<?xml version="1.0"?>
<rss version="2.0">
  <channel>
    <title>GUID Test</title>
    <link>https://example.com</link>
    <description>desc</description>
    <item>
      <title>Has GUID</title>
      <link>https://example.com/guid-item</link>
      <guid isPermaLink="true">https://example.com/guid-item</guid>
      <pubDate>Mon, 01 Jan 2024 12:00:00 +0000</pubDate>
    </item>
  </channel>
</rss>`

	f := &RSSFetcher{}
	items, err := f.parse([]byte(xml))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if items[0].GUID != "https://example.com/guid-item" {
		t.Fatalf("expected guid 'https://example.com/guid-item', got %s", items[0].GUID)
	}
}
