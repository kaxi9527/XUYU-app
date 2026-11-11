package models

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRecipientAndPublicationBindCurrentConnection(t *testing.T) {
	s, _, active := activeGenerator(t, generatorFixture{})
	l, err := s.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	r, err := l.Recipient()
	if err != nil || !r.Valid() || r.Digest() == "" {
		t.Fatal(err)
	}
	b, _ := json.Marshal(r)
	if strings.Contains(string(b), "synthetic-key") || strings.Contains(string(b), "secretRef") {
		t.Fatal("credential in consent preview")
	}
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- l.Publish(context.Background(), func() error { close(entered); <-release; return nil })
	}()
	<-entered
	changed := make(chan error, 1)
	go func() {
		_, err := s.SetMode(context.Background(), active.Revision, "stop-after-publication", "tool", "")
		changed <- err
	}()
	select {
	case <-changed:
		t.Fatal("mode mutation raced publication")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if err = <-changed; err != nil {
		t.Fatal(err)
	}
	if _, err = l.Recipient(); err == nil {
		t.Fatal("stale recipient accepted")
	}
	called := false
	if err = l.Publish(context.Background(), func() error { called = true; return nil }); err == nil || called {
		t.Fatal("stopped lease published")
	}
}
