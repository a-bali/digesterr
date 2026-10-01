package metrics

import (
	"crypto/tls"
	"net/http"
)

// OutboundTransport returns the transport used for every outbound feed
// request: fetchers, enrichers, extraction and comment fetching all reach the
// network through the one transport the Recorder hands out (see
// Server wiring of Poller.SetTransport).
//
// It is http.DefaultTransport with the TLS/ALPN offer pinned to HTTP/1.1.
// news.ycombinator.com's nginx answers 419 ("Sorry", a static 6-byte body, no
// Retry-After) to some client transport shapes on its dynamic paths — /rss,
// /best, /ask, /show, /jobs, /newcomments, /item — while serving the same
// client fine on / and /news. Measured on go1.26.5 against the live host:
// Go's default transport got 419 on /rss (10/10 requests) and 200 with the
// HTTP/1.1 offer (10/10); curl behaves inversely, which is why a curl probe
// cannot clear this. It is not a rate limit (40 back-to-back curl requests to
// /rss all returned 200) and not a User-Agent rule (the configured desktop UA,
// no UA at all, and a full browser UA all behave identically).
//
// The HTTP/1.1 pin is preferred over a TLS 1.3 minimum because the latter is
// global and would break any feed whose origin does not speak TLS 1.3. Feed
// fetching is one request per feed per cycle, so HTTP/2 multiplexing buys
// nothing here.
func OutboundTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.ForceAttemptHTTP2 = false
	// Clearing TLSNextProto stops net/http from re-enabling HTTP/2 for a
	// cloned transport; without it the h2 upgrade comes back.
	t.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
	t.TLSClientConfig = &tls.Config{NextProtos: []string{"http/1.1"}}
	return t
}
