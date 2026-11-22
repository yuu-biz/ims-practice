package accessapi_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/accessapi"
)

const imsi = "001010000000001"

var errInvalid = errors.New("test: invalid message")

type hello struct {
	Name string `json:"name"`
}

const (
	typeHello accessapi.MessageType = "HELLO"
	typeNote  accessapi.MessageType = "NOTE"
	typeBye   accessapi.MessageType = "BYE"
)

// stub is the smallest stub: it answers HELLO, accepts BYE without an
// answer, and refuses a HELLO that names nobody.
type stub struct {
	url     string
	streams *accessapi.Streams
}

func newStub(t *testing.T) *stub {
	t.Helper()
	s := &stub{streams: accessapi.NewStreams("test")}
	router := accessapi.NewRouter(errInvalid, func(err error) int {
		if errors.Is(err, errInvalid) {
			return http.StatusBadRequest
		}
		return 0
	})
	accessapi.HandleMessage(router, typeHello, func(_ context.Context, _ string, m hello) (any, error) {
		if m.Name == "" {
			return nil, errInvalid
		}
		return hello{Name: "re: " + m.Name}, nil
	})
	accessapi.HandleMessage(router, typeBye, func(context.Context, string, hello) (any, error) { return nil, nil })
	router.HandleFunc("GET "+accessapi.EventsPath("{imsi}"), func(w http.ResponseWriter, r *http.Request) {
		s.streams.Serve(w, r, r.PathValue("imsi"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	})
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	s.url = srv.URL
	return s
}

func TestPostAnswersAndRefuses(t *testing.T) {
	s := newStub(t)
	c := accessapi.NewClient("test", s.url, imsi)

	var answer hello
	if err := c.Post(t.Context(), typeHello, hello{Name: "ue"}, &answer); err != nil || answer.Name != "re: ue" {
		t.Errorf("HELLO = %+v, %v; want the answer", answer, err)
	}
	if err := c.Post(t.Context(), typeBye, hello{Name: "ue"}, nil); err != nil {
		t.Errorf("BYE: %v, want 204 with no answer", err)
	}

	var apiErr *accessapi.APIError
	err := c.Post(t.Context(), typeHello, hello{}, &answer)
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadRequest || apiErr.Stub != "test" {
		t.Errorf("HELLO without a name: %v, want 400 from the stub", err)
	}
	// A field the message does not have is a misspelt information
	// element, which the stub refuses.
	err = c.Post(t.Context(), typeHello, map[string]string{"nme": "ue"}, &answer)
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadRequest {
		t.Errorf("unknown field: %v, want 400", err)
	}
	if got := apiErr.Error(); !strings.HasPrefix(got, "test: 400: ") {
		t.Errorf("error text %q, want it to begin with the stub name and status", got)
	}
}

func TestEventsDeliverTheNetworksMessages(t *testing.T) {
	s := newStub(t)
	c := accessapi.NewClient("test", s.url, imsi)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	events, err := c.Events(ctx, map[accessapi.MessageType]func() any{typeNote: func() any { return &hello{} }})
	if err != nil {
		t.Fatal(err)
	}
	// The stub registers the stream a moment after Events returns, so
	// the network tries until the UE is reachable.
	waitSend(t, s.streams, typeBye, hello{Name: "unknown to the client"})
	if err := s.streams.Send(imsi, typeNote, hello{Name: "network"}); err != nil {
		t.Fatal(err)
	}
	select {
	case m := <-events:
		got, ok := m.Message.(*hello)
		if m.Type != typeNote || !ok || got.Name != "network" {
			t.Errorf("event = %+v, want NOTE from the network (BYE is not one the client knows)", m)
		}
	case <-ctx.Done():
		t.Fatal("waited for the NOTE event on the stream")
	}
}

func TestSendToAUEWithoutAStream(t *testing.T) {
	s := accessapi.NewStreams("test")
	if err := s.Send(imsi, typeNote, hello{}); !errors.Is(err, accessapi.ErrUENotReachable) {
		t.Errorf("Send = %v, want ErrUENotReachable", err)
	} else if err.Error() != "test: the UE has no event stream open" {
		t.Errorf("Send = %q, want the name of the stub before the reason", err)
	}
}

func TestANewStreamReplacesTheOldOne(t *testing.T) {
	s := newStub(t)
	c := accessapi.NewClient("test", s.url, imsi)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	messages := map[accessapi.MessageType]func() any{typeNote: func() any { return &hello{} }}

	old, err := c.Events(ctx, messages)
	if err != nil {
		t.Fatal(err)
	}
	waitSend(t, s.streams, typeNote, hello{Name: "first"})
	<-old
	if _, err := c.Events(ctx, messages); err != nil {
		t.Fatal(err)
	}
	select {
	case _, open := <-old:
		if open {
			t.Error("the old stream delivered a message, want it closed")
		}
	case <-ctx.Done():
		t.Fatal("waited for the old stream to close")
	}
}

func waitSend(t *testing.T, s *accessapi.Streams, name accessapi.MessageType, message any) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for s.Send(imsi, name, message) != nil {
		if time.Now().After(deadline) {
			t.Fatalf("waited for the UE's stream to open, to send %s", name)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
