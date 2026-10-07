package httpapi

import (
	"strings"
	"testing"
)

func TestReaderStoredVideoAudioWithoutMutatingItem(t *testing.T) {
	server, queries, db := setupStreamTestServer(t)
	user := createReaderUser(t, queries, "testkey123")
	seed := seedReaderStream(t, queries, db, user.ID)
	legacy := `<p>keep &amp; text</p><video src="https://v.redd.it/2mu8k4a613nh1/CMAF_720.mp4?source=fallback" controls></video>`
	control := `<video src="https://video.twimg.com/control.mp4" controls></video>`
	for id, content := range map[int64]string{seed.aUnreadNewest.ID: legacy, seed.aDigested.ID: control} {
		if _, err := db.Exec("UPDATE items SET content = ?, extract_status = 'none', extract_content = '' WHERE id = ?", content, id); err != nil {
			t.Fatal(err)
		}
	}
	resp := getContents(t, server, "/reader/api/0/stream/items/contents?i="+itemStreamID(seed.aUnreadNewest.ID)+"&i="+itemStreamID(seed.aDigested.ID))
	if len(resp.Items) != 2 {
		t.Fatal("wrong response count")
	}
	body := resp.Items[0].Content.Content
	if resp.Items[0].Summary.Content != body {
		t.Fatal("NetNewsWire summary differs from content")
	}
	if !strings.Contains(body, "HLSPlaylist.m3u8") || strings.Index(body, "HLSPlaylist.m3u8") > strings.Index(body, "CMAF_720.mp4") {
		t.Fatal("reader has no HLS-first audio player", body)
	}
	if !strings.Contains(body, `</video><a href=`) {
		t.Fatal("watch link hidden", body)
	}
	if !strings.Contains(resp.Items[1].Content.Content, control) || strings.Contains(resp.Items[1].Content.Content, "HLSPlaylist") {
		t.Fatal("Twitter control changed")
	}
	var stored string
	if err := db.QueryRow("SELECT content FROM items WHERE id = ?", seed.aUnreadNewest.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != legacy {
		t.Fatal("stored content was mutated")
	}
}
