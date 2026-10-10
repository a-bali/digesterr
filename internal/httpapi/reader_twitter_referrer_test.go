package httpapi

import (
	"strings"
	"testing"
)

// The Reader body for an item carrying a twitter video must carry the no-referrer
// policy, because the client renders it in a web view whose base URL is a
// digesterr URL — and video.twimg.com answers 403 to that Referer. The item's
// stored content must NOT be rewritten: this is a render-time upgrade, matching
// how the Reddit HLS upgrade works.
func TestReaderTwitterVideoDropsReferrer(t *testing.T) {
	server, queries, db := setupStreamTestServer(t)
	user := createReaderUser(t, queries, "testkey123")
	seed := seedReaderStream(t, queries, db, user.ID)

	player := `<p>hi</p><p><video controls preload="metadata" poster="https://pbs.twimg.com/amplify_video_thumb/1/img/x.jpg"><source src="https://video.twimg.com/amplify_video/1/vid/avc1/1280x720/a.mp4?tag=16" type="video/mp4" /></video></p>`
	// Control: a twitter item without a player must not gain the policy, so the
	// rule stays scoped to bodies that actually load gated media.
	control := `<p>no player here</p>`
	for id, content := range map[int64]string{seed.aUnreadNewest.ID: player, seed.aDigested.ID: control} {
		if _, err := db.Exec("UPDATE items SET content = ?, extract_status = 'none', extract_content = '' WHERE id = ?", content, id); err != nil {
			t.Fatal(err)
		}
	}

	resp := getContents(t, server, "/reader/api/0/stream/items/contents?i="+itemStreamID(seed.aUnreadNewest.ID)+"&i="+itemStreamID(seed.aDigested.ID))
	if len(resp.Items) != 2 {
		t.Fatal("wrong response count")
	}

	body := resp.Items[0].Content.Content
	if !strings.Contains(body, `<meta name="referrer" content="no-referrer">`) {
		t.Fatalf("no-referrer policy missing from Reader body: %q", body)
	}
	if strings.Index(body, `name="referrer"`) > strings.Index(body, "<video") {
		t.Fatalf("policy must precede the player: %q", body)
	}
	// NetNewsWire renders summary as the article body, so both fields must agree.
	if resp.Items[0].Summary.Content != body {
		t.Fatal("NetNewsWire summary differs from content")
	}
	if strings.Contains(resp.Items[1].Content.Content, "referrer") {
		t.Fatalf("policy leaked onto an item with no player: %q", resp.Items[1].Content.Content)
	}

	var stored string
	if err := db.QueryRow("SELECT content FROM items WHERE id = ?", seed.aUnreadNewest.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != player {
		t.Fatalf("stored content was mutated: %q", stored)
	}
}
