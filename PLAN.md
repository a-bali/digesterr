# Digesterr

## Introduction

Digesterr will be an application that enables the user to subscribe to various
data sources (feeds). These feeds will be regularly checked and refreshed and
new items will be presented to the user for reading. The user will be able to
browse these items per feed, read them and mark them as read or save for future
reference.

Additionally, Digesterr will regularly create digests for unread items in
selected feeds. These digests will contain an AI-generated summary and highlight
of the items, in order for the user to avoid browsing them one-by-one.

## Architecture

Server-client architecture, with REST API in between.
Use SQL database for storage.
Web UI should be modern JS/CSS, lightweight, responsive and support PWA.

## Tech stack

Database: use SQLite but keep it standard so that other SQL engines can be
added in the future.

Backend: Golang.

Frontend: Vite + Vue + Tailwind.

Tooling: use Nix devenv to create the development environment.

## Development methodology

Follow best practices as per relevant cursor rules (https://cursor.directory).
Always prepare comprehensive test cases using standard tooling and run tests
after each development iteration.

## Phased delivery

1. **Basic backend:** project skeleton, DB schema, user auth, REST API
2. **Feeds:** pluggable feed architecture, RSS/Atom parsing, HN, Lobste.rs and
   Reddit sources. RSS/Atom used for item discovery. Points and comment counts
   fetched via source-specific APIs (Firebase for HN, Lobste.rs JSON API,
   Reddit JSON API). Enrichment runs on poll for unread items.
3. **Basic UI:** 3-pane layout, feed browsing, item reading
4. **Digest and scheduling:** cron-style digest creation, LLM integration
5. **Content extraction:** fetch article content from linked pages using
   readability/clarity, configurable per feed

## Functionality

### User accounts

Multi-user application. Users sign up and sign in with email and password.
Users can change their password. Each user has their own feeds, categories,
digests and items.

### Feeds

Feeds (data sources) should be implemented as pluggable components to allow the
application to source data from all kinds of feeds, regardless of their
implementation specifics.

For starters, the app will support 4 kinds of feeds:

1. RSS/Atom feeds: parse standard-compliant feeds and fetch new items as unread.
   If the item contains a link to comments, that link should also be presented.
2. Hacker News: discover items via RSS feed, enrich with current score and
   comment count via Firebase API (https://hacker-news.firebaseio.com/v0/item/{id}.json).
   Always refetch these when preparing a digest.
3. Lobste.rs: discover items via RSS feed, enrich with current score and
   comment count via Lobste.rs JSON API (https://lobste.rs/s/{short_id}.json).
4. A subreddit in reddit.com: discover items via Atom feed, enrich with current
   score, comment count, and actual article URL via Reddit JSON API
   (https://www.reddit.com/comments/{post_id}.json).

Each feed should be assigned to a category (e.g. tech, lifestyle etc.).

### Feed polling

Feeds are polled globally on a schedule. Default polling interval is every hour.
If a feed is down or returns an error, mark it as failed and retry on the next
scheduled poll.

### Content extraction (Phase 5)

Optionally, the linked article content should be fetched using plain HTTP
(no JS rendering) and extracted using a readability/clarity algorithm.
Whether content is fetched can be configured per feed. The extracted content
is stored in the `content` field of the item and used in digests.

### Item statuses

An item has exactly one of these statuses:
- **unread** — newly fetched, not yet acted on
- **read** — user has read it manually
- **digested** — included in a digest
- **saved** — user explicitly saved it for later

### Digest

The user shall be able to configure digests with the following parameters:
- Timing: cron-style schedule (e.g. daily at 5am, every 4 hours)
- Categories/feeds to include
- Number of highlights to show

The application should prepare these digests as specified, including all new
items from the configured sources. For the highlights, summarize the content
and the top 5-10 comments with LLM, for the rest, just list them with title,
link to content, link to comments.

For the digest, always fetch fresh points/upvotes and list of comments.

Prepared digests should be saved in the database.

### LLM integration

Use any OpenAI-compatible LLM API. The user configures:
- API endpoint URL
- API key (optional — some endpoints don't require one)
- Model name

### UI

The web UI should show a 3-pane setup:
- List of feeds per category
- List of unread/digested/read/all items in the feed
- Content of the selected item

On top, the user should be able to switch the screen between this setup and the
digest view, which should show a list of prepared digests and the content of
the selected one.

The user should have options to add/remove/edit categories, feeds and digests.

A new user starts with no categories and no feeds.