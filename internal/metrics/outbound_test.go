package metrics

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"testing"
)

// trustServer adds a test server's certificate to a transport's root pool so a
// pinned TLSClientConfig can still reach it.
func trustServer(tr *http.Transport, srv *httptest.Server) {
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	tr.TLSClientConfig.RootCAs = pool
}

// TestRecorderTransportPinsHTTP11 is the regression test for the HN RSS 419
// block: news.ycombinator.com's nginx refuses some client transport shapes on
// its dynamic paths, and Go's default transport was one of them, so feed 1
// failed every poll while a curl probe looked healthy.
//
// The Recorder owns the single outbound transport for the whole app, so
// asserting here covers every fetcher, enricher and extractor.
func TestRecorderTransportPinsHTTP11(t *testing.T) {
	var sawMajor int
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawMajor = r.ProtoMajor
		w.Write([]byte("ok"))
	}))
	// Offer h2 on the server: if the client offers it, the connection upgrades
	// and this test fails.
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)

	transport := New(64).Transport()
	trustServer(transport.inner.(*http.Transport), srv)

	client := &http.Client{Transport: transport}
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("request through the recorder transport: %v", err)
	}
	defer resp.Body.Close()

	if sawMajor != 1 {
		t.Errorf("server saw proto major %d, want 1", sawMajor)
	}
	if resp.Proto != "HTTP/1.1" {
		t.Errorf("response protocol = %q, want HTTP/1.1", resp.Proto)
	}
	if resp.TLS != nil && resp.TLS.NegotiatedProtocol == "h2" {
		t.Error("negotiated h2; the outbound transport must not offer it")
	}
}

// TestOutboundTransportDoesNotOfferH2 pins the transport contract directly and
// guards the Clone() trap: cloning http.DefaultTransport and only setting
// ForceAttemptHTTP2=false still reaches h2, because net/http re-enables it from
// the cloned transport's populated TLSNextProto.
func TestOutboundTransportDoesNotOfferH2(t *testing.T) {
	tr := OutboundTransport()

	if tr.ForceAttemptHTTP2 {
		t.Error("ForceAttemptHTTP2 must be false")
	}
	if len(tr.TLSNextProto) != 0 {
		t.Errorf("TLSNextProto must be cleared, got %d entries", len(tr.TLSNextProto))
	}
	if tr.TLSClientConfig == nil {
		t.Fatal("TLSClientConfig must be set to pin the ALPN offer")
	}
	if got := tr.TLSClientConfig.NextProtos; len(got) != 1 || got[0] != "http/1.1" {
		t.Errorf("ALPN offer = %v, want [http/1.1]", got)
	}
	// The default dialer and handshake timeout must survive the clone: pinning
	// the protocol is no reason to lose the connect/TLS bounds.
	if tr.DialContext == nil {
		t.Error("DialContext was dropped; the clone must keep the default dialer")
	}
	if tr.TLSHandshakeTimeout == 0 {
		t.Error("TLSHandshakeTimeout was dropped")
	}
}

// TestOutboundTransportAcceptsTLS12 ensures the HTTP/1.1 pin did not raise the
// TLS floor: a TLS-1.2-only origin must still be reachable, which is why the
// fix deliberately avoids MinVersion: tls.VersionTLS13.
func TestOutboundTransportAcceptsTLS12(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	srv.TLS = &tls.Config{MinVersion: tls.VersionTLS12, MaxVersion: tls.VersionTLS12}
	srv.StartTLS()
	t.Cleanup(srv.Close)

	tr := OutboundTransport()
	trustServer(tr, srv)

	client := &http.Client{Transport: tr}
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("a TLS 1.2-only origin must still be reachable: %v", err)
	}
	defer resp.Body.Close()
	if resp.TLS == nil || resp.TLS.Version != tls.VersionTLS12 {
		t.Error("expected a TLS 1.2 connection")
	}
}
