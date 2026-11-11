package modelhttp

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/models"
)

// The standard-library-only fixture reads complete HTTP/2 DATA frames on a
// loopback TLS connection. No vendor host is contacted, and no extra module is
// required merely to test the transport's implicit POST replay behavior.
func TestGenerationHTTP2DoesNotReplay(t *testing.T) {
	for _, goAway := range []bool{false, true} {
		name := "REFUSED_STREAM"
		if goAway {
			name = "GOAWAY"
		}
		t.Run(name, func(t *testing.T) {
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
						preface := make([]byte, 24)
						if _, err := io.ReadFull(conn, preface); err != nil || string(preface) != "PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n" {
							return
						}
						if writeTestH2Frame(conn, 4, 0, 0, nil) != nil {
							return
						}
						var body bytes.Buffer
						for {
							header := make([]byte, 9)
							if _, err := io.ReadFull(conn, header); err != nil {
								return
							}
							size := int(header[0])<<16 | int(header[1])<<8 | int(header[2])
							if size > 1<<20 {
								return
							}
							payload := make([]byte, size)
							if _, err := io.ReadFull(conn, payload); err != nil {
								return
							}
							stream := binary.BigEndian.Uint32(header[5:]) & 0x7fffffff
							switch header[3] {
							case 4:
								if header[4]&1 == 0 {
									_ = writeTestH2Frame(conn, 4, 1, 0, nil)
								}
							case 0:
								body.Write(payload)
								if header[4]&1 == 0 {
									continue
								}
								if !bytes.Contains(body.Bytes(), []byte("generation-h2-sentinel")) {
									return
								}
								if count.Add(1) == 1 {
									if goAway {
										_ = writeTestH2Frame(conn, 7, 0, 0, make([]byte, 8))
									} else {
										_ = writeTestH2Frame(conn, 3, 0, stream, []byte{0, 0, 0, 7})
									}
								}
								return
							}
						}
					}()
				}
			}()
			defer func() { listener.Close(); wg.Wait() }()
			client := NewClient()
			defer client.Close()
			tr := client.http.Transport.(*http.Transport)
			tr.Proxy = nil
			tr.DisableKeepAlives = true
			tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"h2"}} // ephemeral loopback certificate only
			tr.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "tcp", listener.Addr().String())
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err = client.Generate(ctx, models.Target{Protocol: models.Chat, BaseURL: "https://fixture.invalid/v1", ModelID: "fixture"}, nil, models.GenerationRequest{Input: "generation-h2-sentinel", MaxOutputTokens: 64})
			if count.Load() != 1 || err == nil {
				t.Fatalf("complete payloads=%d error=%v", count.Load(), err)
			}
		})
	}
}

func writeTestH2Frame(w io.Writer, kind, flags byte, stream uint32, payload []byte) error {
	h := make([]byte, 9)
	h[0], h[1], h[2] = byte(len(payload)>>16), byte(len(payload)>>8), byte(len(payload))
	h[3], h[4] = kind, flags
	binary.BigEndian.PutUint32(h[5:], stream)
	if _, err := w.Write(h); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}
