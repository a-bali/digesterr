package feed

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	readability "codeberg.org/readeck/go-readability/v2"
	"github.com/yuin/goldmark"
)

// jinaReaderURL is a var (not a const) so tests can point the Jina fallback
// at an httptest server instead of the real service.
var jinaReaderURL = "https://r.jina.ai"

func ExtractWithJina(ctx context.Context, pageURL string, client *http.Client, logger *slog.Logger) (string, error) {
	target := jinaReaderURL + "/" + url.QueryEscape(pageURL)
	req, err := http.NewRequestWithContext(ctx, "GET", target, nil)
	if err != nil {
		return "", fmt.Errorf("creating jina request: %w", err)
	}
	req.Header.Set("Accept", "text/plain")
	req.Header.Set("X-Cache-Bypass", "1")
	req.Header.Set("X-Return-Format", "markdown")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("jina request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("jina HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 500<<10))
	if err != nil {
		return "", fmt.Errorf("reading jina response: %w", err)
	}

	md := string(bytes.TrimSpace(body))
	if len(md) < 10 {
		return "", fmt.Errorf("jina returned too short content (%d bytes)", len(md))
	}

	var htmlBuf bytes.Buffer
	if err := goldmark.Convert(body, &htmlBuf); err != nil {
		return "", fmt.Errorf("markdown conversion: %w", err)
	}

	return htmlBuf.String(), nil
}

func htmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	return s
}

// isRedditHostedURL reports whether the URL points to content hosted on
// Reddit itself (reddit.com, *.reddit.com, *.redd.it). Content extraction
// should be skipped for these because the Reddit API already provides the
// full content, and fetching the web page returns comment tree HTML instead.
func isRedditHostedURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "www.reddit.com" || host == "reddit.com" ||
		strings.HasSuffix(host, ".reddit.com") ||
		host == "redd.it" || strings.HasSuffix(host, ".redd.it")
}

func ExtractContent(ctx context.Context, pageURL string, client *http.Client, logger *slog.Logger) (string, error) {
	if isRedditHostedURL(pageURL) {
		logger.Debug("content extraction: skipping reddit-hosted URL", "url", pageURL)
		return "", nil
	}
	req, err := http.NewRequestWithContext(ctx, "GET", pageURL, nil)
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("User-Agent", DefaultUserAgent)

	resp, err := client.Do(req)
	if err != nil {
		logger.Info("content extraction: direct fetch failed, trying Jina.ai", "error", err)
		html, jerr := ExtractWithJina(ctx, pageURL, client, logger)
		if jerr != nil {
			logger.Info("content extraction: jina failed", "error", jerr)
			return "", jerr
		}
		logger.Debug("content extraction: jina succeeded", "bytes", len(html))
		return html, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logger.Info("content extraction: direct fetch failed, trying Jina.ai", "status", resp.StatusCode)
		html, jerr := ExtractWithJina(ctx, pageURL, client, logger)
		if jerr != nil {
			logger.Info("content extraction: jina failed", "error", jerr)
			return "", jerr
		}
		logger.Debug("content extraction: jina succeeded", "bytes", len(html))
		return html, nil
	}

	contentType := resp.Header.Get("Content-Type")
	contentType = strings.ToLower(contentType)

	switch {
	case strings.HasPrefix(contentType, "text/html") || strings.HasPrefix(contentType, "application/xhtml+xml"):
		start := time.Now()
		out, err := extractHTMLContent(ctx, resp.Body, pageURL, client, logger)
		logger.Debug("content extraction: direct fetch complete", "content_type", contentType, "bytes", len(out), "duration", time.Since(start))
		return out, err

	case strings.HasPrefix(contentType, "image/"):
		return fmt.Sprintf(`<img src="%s" style="max-width:100%%" />`, htmlEscape(pageURL)), nil

	case strings.HasPrefix(contentType, "video/"):
		return fmt.Sprintf(
			`<video src="%s" controls style="max-width:100%%"><p>Your browser does not support the video tag. <a href="%s" target="_blank">Watch video ↗</a></p></video>`,
			htmlEscape(pageURL), htmlEscape(pageURL)), nil

	case strings.HasPrefix(contentType, "audio/"):
		return fmt.Sprintf(
			`<audio src="%s" controls><p>Your browser does not support the audio tag. <a href="%s" target="_blank">Listen ↗</a></p></audio>`,
			htmlEscape(pageURL), htmlEscape(pageURL)), nil

	case strings.HasPrefix(contentType, "text/"):
		body, err := io.ReadAll(io.LimitReader(resp.Body, 100<<10))
		if err != nil {
			return "", fmt.Errorf("read body: %w", err)
		}
		logger.Debug("content extraction: plain text captured", "bytes", len(body))
		return fmt.Sprintf(`<pre style="white-space:pre-wrap">%s</pre>`, htmlEscape(string(body))), nil

	case strings.HasPrefix(contentType, "application/pdf"):
		logger.Info("content extraction: pdf detected, trying Jina.ai")
		html, err := ExtractWithJina(ctx, pageURL, client, logger)
		if err != nil {
			logger.Info("content extraction: jina failed for pdf, falling back to browser link", "error", err)
			return fmt.Sprintf(`<p><a href="%s" target="_blank">Open in browser ↗</a></p>`, htmlEscape(pageURL)), nil
		}
		return html, nil

	default:
		return fmt.Sprintf(`<p><a href="%s" target="_blank">Open in browser ↗</a></p>`, htmlEscape(pageURL)), nil
	}
}

func extractHTMLContent(ctx context.Context, body io.Reader, pageURL string, client *http.Client, logger *slog.Logger) (string, error) {
	reqURL, _ := url.Parse(pageURL)
	article, err := readability.FromReader(body, reqURL)
	if err == nil {
		var buf bytes.Buffer
		if rerr := article.RenderHTML(&buf); rerr == nil && buf.Len() > 0 {
			logger.Debug("content extraction: readability succeeded", "bytes", buf.Len())
			return buf.String(), nil
		} else {
			err = rerr
		}
	}

	logger.Info("content extraction: readability failed", "error", err)

	start := time.Now()
	html, jerr := ExtractWithJina(ctx, pageURL, client, logger)
	if jerr != nil {
		logger.Info("content extraction: jina failed", "error", jerr)
		return "", jerr
	}
	logger.Debug("content extraction: jina succeeded", "bytes", len(html), "duration", time.Since(start))
	return html, nil
}
