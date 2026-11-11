package gateway

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
)

// Independent review fixture; uses an ephemeral TLS certificate and loopback
// only. The native-looking URL is dialed exclusively to our local listener.
func TestWorkShelfHTTP2DoesNotReplay(t *testing.T) {
	seed := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	cert := seed.TLS.Certificates[0]
	seed.Close()
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: []string{"h2"}})
	if err != nil {
		t.Fatal(err)
	}
	var count atomic.Int32
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			func() {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(4 * time.Second))
				preface := make([]byte, len(http2.ClientPreface))
				if _, err := io.ReadFull(conn, preface); err != nil {
					return
				}
				f := http2.NewFramer(conn, conn)
				if f.WriteSettings() != nil {
					return
				}
				var body bytes.Buffer
				for {
					frame, err := f.ReadFrame()
					if err != nil {
						return
					}
					switch frame := frame.(type) {
					case *http2.SettingsFrame:
						if !frame.IsAck() {
							_ = f.WriteSettingsAck()
						}
					case *http2.DataFrame:
						body.Write(frame.Data())
						if !frame.StreamEnded() {
							continue
						}
						if !strings.Contains(body.String(), "review-h2-sentinel") {
							return
						}
						if count.Add(1) == 1 {
							_ = f.WriteRSTStream(frame.StreamID, http2.ErrCodeRefusedStream)
							return
						}
						var block bytes.Buffer
						e := hpack.NewEncoder(&block)
						_ = e.WriteField(hpack.HeaderField{Name: ":status", Value: "200"})
						_ = e.WriteField(hpack.HeaderField{Name: "content-type", Value: "application/json"})
						_ = f.WriteHeaders(http2.HeadersFrameParam{StreamID: frame.StreamID, BlockFragment: block.Bytes(), EndHeaders: true})
						_ = f.WriteData(frame.StreamID, true, []byte(`{"result":"fixture"}`))
						return
					}
				}
			}()
		}
	}()
	defer func() { listener.Close(); wg.Wait() }()
	tr := vendorTransport(3 * time.Second)
	tr.DisableKeepAlives = true
	tr.Proxy = nil
	tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"h2"}} // local fixture only
	tr.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", listener.Addr().String())
	}
	defer tr.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://api.githubcopilot.com/chat/completions", strings.NewReader(`{"max_completion_tokens":64,"messages":[{"role":"user","content":"review-h2-sentinel"}]}`))
	p := workShelfFixture("copilot")
	res, sendErr := workShelfSend(p, req, &workShelfTextLimit{max: 64, agent: p.ID, user: p.Account.User, control: workShelfBoundaryPermit()}, &http.Client{Transport: tr})
	if res != nil {
		io.Copy(io.Discard, res.Body)
		res.Body.Close()
	}
	if count.Load() != 1 {
		t.Fatalf("one workShelfSend emitted %d complete sentinel bodies; error=%v", count.Load(), sendErr)
	}
}
