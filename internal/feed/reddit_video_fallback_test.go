package feed

import (
	"strings"
	"testing"
)

// TestParseListingCrosspostVideoUsesParentFallback pins the second half of the
// 2026-10-01 prod report (r/hungary 1wuum4u -> item 701205): on a crossposted
// video, Reddit nulls the CROSSPOST's media/is_video and puts the playable
// variant on the parent. The fetcher only consulted p.Media.RedditVideo for the
// post being rendered, so the parent's fallback_url was dropped and the item was
// stored pointing at the bare v.redd.it URL — which serves no media at all (a
// 302 to the post page, then 403), so the <video> can never play, CSP or no CSP.
//
// JSON below is the live shape: outer is_video=false, media=null,
// crosspost_parent_list[0] is_video=true with media.reddit_video present.
func TestParseListingCrosspostVideoUsesParentFallback(t *testing.T) {
	listingJSON := `{
		"data": {
			"children": [{
				"kind": "t3",
				"data": {
					"id": "1wuum4u",
					"title": "Hankó volt államtitkára is megérkezett",
					"author": "someone",
					"url": "https://v.redd.it/frm6xwepntsh1",
					"permalink": "/r/hungary/comments/1wuum4u/hanko/",
					"selftext": "",
					"score": 591,
					"num_comments": 105,
					"created_utc": 1788972970.0,
					"is_self": false,
					"is_video": false,
					"media": null,
					"over_18": false,
					"stickied": false,
					"crosspost_parent": "t3_1wsomeparent",
					"crosspost_parent_list": [{
						"id": "1wsomeparent",
						"title": "Hankó volt államtitkára is megérkezett",
						"author": "op",
						"url": "https://v.redd.it/frm6xwepntsh1",
						"permalink": "/r/magyar/comments/1wsomeparent/hanko/",
						"selftext": "",
						"score": 12,
						"num_comments": 3,
						"created_utc": 1788971000.0,
						"is_self": false,
						"is_video": true,
						"over_18": false,
						"stickied": false,
						"media": {
							"reddit_video": {
								"fallback_url": "https://v.redd.it/frm6xwepntsh1/CMAF_1080.mp4?source=fallback",
								"height": 1888,
								"width": 1080,
								"is_gif": false
							}
						}
					}]
				}
			}]
		}
	}`

	f := &RedditFetcher{}
	items, err := f.parseListing([]byte(listingJSON), "hungary")
	if err != nil {
		t.Fatalf("parseListing: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	content := items[0].Content

	// The playable variant must come from the PARENT's payload.
	if !strings.Contains(content, `src="https://v.redd.it/frm6xwepntsh1/CMAF_1080.mp4?source=fallback"`) {
		t.Errorf("crossposted video did not use the parent's fallback_url: %s", content)
	}
	// ...and the bare v.redd.it URL must never be the <video> source: it serves
	// no media (302 -> reddit.com/video/<id> -> 403).
	if strings.Contains(content, `src="https://v.redd.it/frm6xwepntsh1"`) {
		t.Errorf("video src is the unplayable bare v.redd.it URL: %s", content)
	}
	// A crosspost's own payload is a stub, so rendering both payloads produced
	// TWO players for the same video (one dead, one working). Exactly one.
	if n := strings.Count(content, "<video"); n != 1 {
		t.Errorf("got %d <video> elements, want exactly 1: %s", n, content)
	}
}

// TestParseListingCrosspostVideoWithoutFallbackDegradesToLink covers the case
// where neither the crosspost nor its parent exposes media.reddit_video (Reddit
// strips media from some posts). A <video src="https://v.redd.it/<id>"> is a
// dead player — that URL answers with a redirect to the post's HTML page — so
// the item must degrade to a plain link instead of an empty player.
func TestParseListingCrosspostVideoWithoutFallbackDegradesToLink(t *testing.T) {
	listingJSON := `{
		"data": {
			"children": [{
				"kind": "t3",
				"data": {
					"id": "1wnofb",
					"title": "No media anywhere",
					"author": "someone",
					"url": "https://v.redd.it/nofallback",
					"permalink": "/r/hungary/comments/1wnofb/nomedia/",
					"selftext": "",
					"score": 5,
					"num_comments": 0,
					"created_utc": 1788972970.0,
					"is_self": false,
					"is_video": false,
					"media": null,
					"over_18": false,
					"stickied": false,
					"crosspost_parent_list": [{
						"id": "1wnofb",
						"title": "No media anywhere",
						"url": "https://v.redd.it/nofallback",
						"permalink": "/r/other/comments/1wnofb/nomedia/",
						"selftext": "",
						"is_self": false,
						"is_video": false,
						"media": null,
						"score": 1,
						"num_comments": 0,
						"created_utc": 1788971000.0
					}]
				}
			}]
		}
	}`

	f := &RedditFetcher{}
	items, err := f.parseListing([]byte(listingJSON), "hungary")
	if err != nil {
		t.Fatalf("parseListing: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	content := items[0].Content
	if strings.Contains(content, "<video") {
		t.Errorf("dead player rendered for a video with no fallback: %s", content)
	}
	if !strings.Contains(content, `href="https://v.redd.it/nofallback"`) {
		t.Errorf("item did not degrade to a link: %s", content)
	}
}

// TestParseListingOwnVideoUsesFallback is the control for the test above: an
// ordinary (non-crossposted) video post must still resolve its own fallback_url.
func TestParseListingOwnVideoUsesFallback(t *testing.T) {
	listingJSON := `{
		"data": {
			"children": [{
				"kind": "t3",
				"data": {
					"id": "1wvid",
					"title": "Own video",
					"author": "someone",
					"url": "https://v.redd.it/ownvid",
					"permalink": "/r/hungary/comments/1wvid/own/",
					"selftext": "",
					"score": 10,
					"num_comments": 1,
					"created_utc": 1788972970.0,
					"is_self": false,
					"is_video": true,
					"over_18": false,
					"stickied": false,
					"media": {
						"reddit_video": {
							"fallback_url": "https://v.redd.it/ownvid/CMAF_720.mp4?source=fallback",
							"height": 1280,
							"width": 720,
							"is_gif": false
						}
					}
				}
			}]
		}
	}`

	f := &RedditFetcher{}
	items, err := f.parseListing([]byte(listingJSON), "hungary")
	if err != nil {
		t.Fatalf("parseListing: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	if !strings.Contains(items[0].Content, `src="https://v.redd.it/ownvid/CMAF_720.mp4?source=fallback"`) {
		t.Errorf("own video did not use its fallback_url: %s", items[0].Content)
	}
}
