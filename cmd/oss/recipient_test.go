package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/mns"
)

func newTestRecipient(t *testing.T) (*recipient, *httptest.Server) {
	t.Helper()
	r := newRecipient(slog.New(slog.DiscardHandler))
	r.now = func() time.Time { return time.Date(2026, 11, 24, 10, 15, 3, 0, time.UTC) }
	server := httptest.NewServer(r.Handler())
	t.Cleanup(server.Close)
	return r, server
}

// post sends body as the OAM would, and returns the status and the body
// of the answer.
func post(t *testing.T, server *httptest.Server, contentType, body string) (int, string) {
	t.Helper()
	resp, err := http.Post(server.URL+notificationPath, contentType, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(b)
}

func get[T any](t *testing.T, url string) T {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("GET %s: %s, %s", url, resp.Status, resp.Header.Get("Content-Type"))
	}
	var v T
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// A notification taken is answered with 204 and no body, and read back
// in the order it came, as it came.
func TestRecipientKeepsTheNotificationsInOrder(t *testing.T) {
	_, server := newTestRecipient(t)
	for _, body := range []string{notifyNewAlarm, notifyAckStateChanged, notifyClearedAlarm} {
		if status, answer := post(t, server, "application/json", body); status != http.StatusNoContent || answer != "" {
			t.Fatalf("answer %d %q, want 204 and no body", status, answer)
		}
	}
	all := get[[]received](t, server.URL+"/received")
	var types []mns.NotificationType
	for _, n := range all {
		types = append(types, n.NotificationType)
	}
	if want := []mns.NotificationType{mns.NotifyNewAlarmType, mns.NotifyAckStateChangedType, mns.NotifyClearedAlarmType}; !slices.Equal(types, want) {
		t.Errorf("received %q, want %q", types, want)
	}
	var first mns.NotifyNewAlarm
	if err := json.Unmarshal(all[0].Notification, &first); err != nil || first.AlarmId != "scscf.9f3c2a7b1d4e6058" || first.NotificationId != 4711 {
		t.Errorf("the first notification reads back as %+v, %v", first, err)
	}
	if !all[0].ReceivedAt.Equal(time.Date(2026, 11, 24, 10, 15, 3, 0, time.UTC)) {
		t.Errorf("received_at %v", all[0].ReceivedAt)
	}

	acks := get[[]received](t, server.URL+"/received?notification_type=notifyAckStateChanged")
	if len(acks) != 1 || acks[0].NotificationType != mns.NotifyAckStateChangedType {
		t.Errorf("the notifyAckStateChanged are %+v", acks)
	}
	if none := get[[]received](t, server.URL+"/received?notification_type=notifyAlarmListRebuilt"); none == nil || len(none) != 0 {
		t.Errorf("no notifyAlarmListRebuilt reads as %#v, want []", none)
	}
	if refused := get[[]refused](t, server.URL+"/refused"); len(refused) != 0 {
		t.Errorf("refused %+v", refused)
	}
}

// A body that is not one the OAM may send is answered with an error
// and an ErrorResponse, kept apart with the reason, and not among the
// notifications.
func TestRecipientRefusesWhatItCannotTake(t *testing.T) {
	r, server := newTestRecipient(t)
	for _, c := range []struct {
		contentType, body string
		status            int
	}{
		{"application/json", edit(t, notifyNewAlarm, "reason", `"indeterminate"`), http.StatusBadRequest},
		{"application/json", `{}`, http.StatusBadRequest},
		{"text/plain", notifyNewAlarm, http.StatusUnsupportedMediaType},
		{"application/json", `{"x": "` + strings.Repeat("a", maxNotification) + `"}`, http.StatusRequestEntityTooLarge},
	} {
		status, answer := post(t, server, c.contentType, c.body)
		var e mns.ErrorResponse
		if err := decodeStrictly([]byte(answer), &e); status != c.status || err != nil || e.Error.ErrorInfo == "" {
			t.Errorf("%.40s as %s: answer %d %s, want %d with an ErrorResponse", c.body, c.contentType, status, answer, c.status)
		}
	}
	refused := get[[]refused](t, server.URL+"/refused")
	if len(refused) != 4 || refused[0].Status != http.StatusBadRequest || !strings.Contains(refused[0].Reason, `"reason" at / is not an attribute that oss takes`) || !strings.Contains(refused[0].Body, `"reason"`) {
		t.Errorf("refused %+v", refused)
	}
	if got := get[[]received](t, server.URL+"/received"); len(got) != 0 {
		t.Errorf("received %+v", got)
	}
	if len(r.received) != 0 {
		t.Errorf("kept %+v", r.received)
	}
}

func TestRecipientRoutes(t *testing.T) {
	_, server := newTestRecipient(t)
	for _, c := range []struct {
		method, path string
		status       int
	}{
		{http.MethodGet, notificationPath, http.StatusMethodNotAllowed},
		{http.MethodPut, notificationPath, http.StatusMethodNotAllowed},
		{http.MethodPost, "/received", http.StatusMethodNotAllowed},
		{http.MethodGet, "/received?notificationType=notifyNewAlarm", http.StatusBadRequest},
		{http.MethodGet, "/", http.StatusNotFound},
		{http.MethodPost, "/notification", http.StatusNotFound},
	} {
		req, err := http.NewRequest(c.method, server.URL+c.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		var e mns.ErrorResponse
		if resp.StatusCode != c.status || resp.Header.Get("Content-Type") != "application/json" || decodeStrictly(body, &e) != nil || e.Error.ErrorInfo == "" {
			t.Errorf("%s %s: %s, %s %s; want %d with an ErrorResponse", c.method, c.path, resp.Status, resp.Header.Get("Content-Type"), body, c.status)
		}
		if c.status == http.StatusMethodNotAllowed && resp.Header.Get("Allow") == "" {
			t.Errorf("%s %s: 405 with no Allow", c.method, c.path)
		}
	}
}

// Notifications that come at the same time are all kept, each once.
func TestRecipientTakesNotificationsAtTheSameTime(t *testing.T) {
	_, server := newTestRecipient(t)
	const senders = 20
	var wg sync.WaitGroup
	// Every other body is one to refuse.
	bodies := make([]string, senders)
	for i := range bodies {
		bodies[i] = edit(t, notifyNewAlarm, "notificationId", strconv.Itoa(i))
		if i%2 == 1 {
			bodies[i] = edit(t, bodies[i], "sequenceNo", "1")
		}
	}
	for _, body := range bodies {
		wg.Go(func() {
			resp, err := http.Post(server.URL+notificationPath, "application/json", strings.NewReader(body))
			if err != nil {
				t.Error(err)
				return
			}
			resp.Body.Close()
		})
		wg.Go(func() {
			if resp, err := http.Get(server.URL + "/received"); err == nil {
				resp.Body.Close()
			}
		})
	}
	wg.Wait()
	ids := map[int64]bool{}
	for _, n := range get[[]received](t, server.URL+"/received") {
		var header mns.NotificationHeader
		if err := json.Unmarshal(n.Notification, &header); err != nil {
			t.Fatal(err)
		}
		ids[header.NotificationId] = true
	}
	if refused := get[[]refused](t, server.URL+"/refused"); len(ids) != senders/2 || len(refused) != senders/2 {
		t.Errorf("kept %d notifications and %d refused bodies, want %d of each", len(ids), len(refused), senders/2)
	}
}

// Of a refused body, the recipient keeps the start and the length.
func TestRecipientKeepsTheStartOfARefusedBody(t *testing.T) {
	_, server := newTestRecipient(t)
	body := edit(t, notifyNewAlarm, "additionalText", `"`+strings.Repeat("a", 4*maxRefusedBody)+`"`)
	body = edit(t, body, "sequenceNo", "1")
	if status, _ := post(t, server, "application/json", body); status != http.StatusBadRequest {
		t.Fatalf("answer %d", status)
	}
	refused := get[[]refused](t, server.URL+"/refused")
	if len(refused) != 1 || refused[0].Body != body[:maxRefusedBody] || refused[0].BodyLength != len(body) {
		t.Errorf("kept %d bytes of %d, with the length %d", len(refused[0].Body), len(body), refused[0].BodyLength)
	}
}

// The recipient keeps the last notifications only, so that one left
// running does not grow without end.
func TestRecipientKeepsTheLastNotifications(t *testing.T) {
	list := []int{}
	for i := range keep + 5 {
		list = appendKept(list, i)
	}
	if len(list) != keep || list[0] != 5 || list[keep-1] != keep+4 {
		t.Errorf("kept %d, from %d to %d", len(list), list[0], list[len(list)-1])
	}
}
