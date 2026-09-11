package feed

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/mmcdole/gofeed/atom"
	"github.com/mmcdole/gofeed/rss"
)

type RSSFetcher struct {
	userAgent string
	client     *http.Client
}

func (f *RSSFetcher) setTransport(t http.RoundTripper) { f.client.Transport = t }

func NewRSSFetcher() *RSSFetcher {
	return &RSSFetcher{
		userAgent: DefaultUserAgent,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (f *RSSFetcher) Fetch(ctx context.Context, feedURL string) ([]Item, error) {
	body, err := f.fetchBody(ctx, feedURL)
	if err != nil {
		return nil, err
	}
	return f.parse(body)
}

// fetchBody performs the HTTP GET shared by Fetch and FeedTitle.
func (f *RSSFetcher) fetchBody(ctx context.Context, feedURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, feedURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("User-Agent", f.userAgent)

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching feed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("feed returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}
	return body, nil
}

// FeedTitle fetches the feed and returns the title it declares for itself:
// the RSS channel title or the Atom feed title. Callers use it to prefill a
// feed's name when the user only supplied a URL.
func (f *RSSFetcher) FeedTitle(ctx context.Context, feedURL string) (string, error) {
	body, err := f.fetchBody(ctx, feedURL)
	if err != nil {
		return "", err
	}
	switch detectFeedFormat(body) {
	case "rss":
		feed, err := (&rss.Parser{}).Parse(bytes.NewReader(body))
		if err != nil {
			return "", fmt.Errorf("parsing RSS: %w", err)
		}
		return stripHTMLTags(strings.TrimSpace(feed.Title)), nil
	case "atom":
		feed, err := (&atom.Parser{}).Parse(bytes.NewReader(body))
		if err != nil {
			return "", fmt.Errorf("parsing Atom: %w", err)
		}
		return stripHTMLTags(strings.TrimSpace(feed.Title)), nil
	default:
		return "", fmt.Errorf("unsupported feed format: %s", detectFeedFormat(body))
	}
}

func (f *RSSFetcher) parse(data []byte) ([]Item, error) {
	format := detectFeedFormat(data)
	switch format {
	case "rss":
		return f.parseRSS(data)
	case "atom":
		return f.parseAtom(data)
	default:
		return nil, fmt.Errorf("unsupported feed format: %s", format)
	}
}

func (f *RSSFetcher) parseRSS(data []byte) ([]Item, error) {
	parser := &rss.Parser{}
	feed, err := parser.Parse(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("parsing RSS: %w", err)
	}

	var items []Item
	for _, rssItem := range feed.Items {
		item := Item{
			Title:   stripHTMLTags(strings.TrimSpace(rssItem.Title)),
			URL:     strings.TrimSpace(rssItem.Link),
			Content: rssContent(rssItem),
			Author:  rssAuthor(rssItem),
		}

		if rssItem.GUID != nil {
			item.GUID = strings.TrimSpace(rssItem.GUID.Value)
		}
		if item.GUID == "" {
			item.GUID = item.URL
		}

		item.CommentURL = strings.TrimSpace(rssItem.Comments)

		if rssItem.PubDate != "" {
			if t, err := parseDate(rssItem.PubDate); err == nil {
				item.PublishedAt = t
			} else {
				slog.Debug("failed to parse RSS date", "date", rssItem.PubDate, "error", err)
			}
		}

		items = append(items, item)
	}
	return items, nil
}

func (f *RSSFetcher) parseAtom(data []byte) ([]Item, error) {
	parser := &atom.Parser{}
	feed, err := parser.Parse(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("parsing Atom: %w", err)
	}

	var items []Item
	for _, entry := range feed.Entries {
		item := Item{
			Title:   stripHTMLTags(strings.TrimSpace(entry.Title)),
			Content: atomContent(entry),
			Author:  atomAuthor(entry),
		}

		item.GUID = strings.TrimSpace(entry.ID)
		if item.GUID == "" {
			item.GUID = item.URL
		}

		for _, link := range entry.Links {
			href := strings.TrimSpace(link.Href)
			if href == "" {
				continue
			}
			switch link.Rel {
			case "alternate", "":
				if item.URL == "" {
					item.URL = href
				}
			case "comments":
				if item.CommentURL == "" {
					item.CommentURL = href
				}
			}
		}

		published := entry.Published
		if published == "" {
			published = entry.Updated
		}
		if published != "" {
			if t, err := parseDate(published); err == nil {
				item.PublishedAt = t
			} else {
				slog.Debug("failed to parse Atom date", "date", published, "error", err)
			}
		}

		items = append(items, item)
	}
	return items, nil
}

func rssContent(item *rss.Item) string {
	if s := strings.TrimSpace(item.Content); s != "" {
		return s
	}
	return strings.TrimSpace(item.Description)
}

func rssAuthor(item *rss.Item) string {
	if s := strings.TrimSpace(item.Author); s != "" {
		return s
	}
	if item.Extensions != nil {
		if dc, ok := item.Extensions["dc"]["creator"]; ok && len(dc) > 0 {
			return strings.TrimSpace(dc[0].Value)
		}
	}
	return ""
}

func atomContent(entry *atom.Entry) string {
	if entry.Content != nil {
		if v := strings.TrimSpace(entry.Content.Value); v != "" {
			return v
		}
	}
	return strings.TrimSpace(entry.Summary)
}

func atomAuthor(entry *atom.Entry) string {
	if len(entry.Authors) > 0 {
		if s := strings.TrimSpace(entry.Authors[0].Name); s != "" {
			return s
		}
	}
	return ""
}

func detectFeedFormat(data []byte) string {
	peek := data
	if len(peek) > 500 {
		peek = peek[:500]
	}
	lower := strings.ToLower(string(peek))
	if strings.Contains(lower, "<rss") {
		return "rss"
	}
	if strings.Contains(lower, "<feed") {
		return "atom"
	}
	if strings.Contains(lower, "<rdf:rdf") {
		return "rdf"
	}
	slog.Debug("unable to detect feed format from XML content", "preview", string(peek[:min(len(peek), 100)]))
	return "unknown"
}

var dateFormats = []string{
	time.RFC1123Z,
	time.RFC1123,
	time.RFC3339,
	time.RFC822Z,
	time.RFC822,
	time.ANSIC,
	time.DateTime,
	"2006-01-02T15:04:05-07:00",
	"2006-01-02T15:04:05Z",
	"2006-01-02 15:04:05 -0700",
	"2006-01-02 15:04:05 MST",
	"2006-01-02",
	"02 Jan 2006 15:04:05 -0700",
	"02 Jan 2006 15:04:05 MST",
	"Mon, 2 Jan 2006 15:04:05 -0700",
	"Mon, 2 Jan 2006 15:04:05 MST",
}

func parseDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, layout := range dateFormats {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unable to parse date: %s", s)
}

// SetUserAgent overrides the User-Agent header for this fetcher.
func (f *RSSFetcher) SetUserAgent(ua string) { f.userAgent = ua }
