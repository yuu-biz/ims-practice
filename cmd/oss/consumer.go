package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"

	"github.com/yuu-biz/ims-practice/internal/mns"
)

// The media type of the JSON Merge Patch (RFC 7396) with which the OSS
// acknowledges alarms. The OAM takes no other patch form.
const mergePatch = "application/merge-patch+json"

// alarmListId is the id of the one AlarmList of the SubNetwork, which
// the OAM fixes (TS 28.111 7.3.2.1).
const alarmListId = "1"

// maxAnswer is the largest answer the consumer reads. An alarm list of
// every node at its limit of records is a few megabytes.
const maxAnswer = 16 << 20

// maxErrorAnswer is how much of an answer an error message shows.
const maxErrorAnswer = 1 << 10

// consumer is the OSS as an MnS consumer: it sends the ProvMnS requests
// of TS 28.532 12.1.1.1 to the OAM, under {MnSRoot}/ProvMnS/v19, for
// the objects of one SubNetwork.
type consumer struct {
	root       mns.MnSRoot
	subNetwork mns.DN
	client     *http.Client
}

func (c consumer) subscription(id string) mns.DN {
	return c.subNetwork.Child(mns.ClassNtfSubscriptionControl, id)
}

func (c consumer) alarmList() mns.DN {
	return c.subNetwork.Child(mns.ClassAlarmList, alarmListId)
}

// subscribe creates the NtfSubscriptionControl id below the SubNetwork,
// or replaces the one of that id, with createMOI as a PUT (TS 28.532
// 12.1.1.1.2.1). The representation carries the id and the object class
// (TS 32.158 5.1.2). The OAM answers with the subscription it now holds,
// with 201 when it created it and 200 when it replaced it, or with 204
// and no body when it held the same subscription already (5.1.2); the
// subscription is then the one sent.
func (c consumer) subscribe(ctx context.Context, id string, attributes mns.NtfSubscriptionControlAttributes) (mns.NtfSubscriptionControlSingle, bool, error) {
	sent := mns.NtfSubscriptionControlSingle{Top: mns.Top{Id: id, ObjectClass: mns.ClassNtfSubscriptionControl}, Attributes: &attributes}
	var created mns.NtfSubscriptionControlSingle
	body, err := json.Marshal(sent)
	if err != nil {
		return created, false, err
	}
	resp, answer, err := c.do(ctx, http.MethodPut, c.root.ResourceURI(c.subscription(id)), "application/json", body)
	if err != nil {
		return created, false, err
	}
	switch resp.StatusCode {
	case http.StatusNoContent:
		return sent, false, nil
	case http.StatusCreated, http.StatusOK:
	default:
		return created, false, failed[mns.ErrorResponseDefault](resp, answer)
	}
	if err := decodeAnswer(resp, answer, &created); err != nil {
		return created, false, err
	}
	if created.Id != id || created.Attributes == nil {
		return created, false, fmt.Errorf("%s %s: the answer is not the subscription %s: %s", resp.Request.Method, resp.Request.URL, id, excerpt(answer))
	}
	return created, resp.StatusCode == http.StatusCreated, nil
}

// unsubscribe deletes the NtfSubscriptionControl id, with deleteMOI
// (TS 28.532 12.1.1.1.5), which ends the subscription.
func (c consumer) unsubscribe(ctx context.Context, id string) error {
	resp, answer, err := c.do(ctx, http.MethodDelete, c.root.ResourceURI(c.subscription(id)), "", nil)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusNoContent {
		return failed[mns.ErrorResponseDefault](resp, answer)
	}
	return nil
}

// readAlarmList reads the AlarmList with getMOIAttributes (TS 28.532
// 12.1.1.1.3). filter, when not empty, is a Jex conditions expression
// that selects the alarm records, such as
// /AlarmList[id="1"]/attributes/alarmRecords/*[perceivedSeverity="MAJOR"]
// (TS 28.111 A.1.4).
func (c consumer) readAlarmList(ctx context.Context, filter string) (mns.AlarmListSingle, error) {
	var list mns.AlarmListSingle
	uri := c.root.ResourceURI(c.alarmList())
	if filter != "" {
		uri += "?" + url.Values{"filter": {filter}}.Encode()
	}
	resp, answer, err := c.do(ctx, http.MethodGet, uri, "", nil)
	if err != nil {
		return list, err
	}
	if resp.StatusCode != http.StatusOK {
		return list, failed[mns.ErrorResponseGet](resp, answer)
	}
	if err := decodeAnswer(resp, answer, &list); err != nil {
		return list, err
	}
	if list.Id != alarmListId || list.Attributes == nil {
		return list, fmt.Errorf("GET %s: the answer is not the AlarmList %s: %s", uri, alarmListId, excerpt(answer))
	}
	return list, nil
}

// ackPatch is the JSON Merge Patch of the AlarmList that sets the
// acknowledgement of alarm records (TS 28.111 A.1.4, TS 32.158 6.3.2).
// It writes only the attributes that the OSS may change: mns.AlarmRecord
// would write every attribute of the record.
type ackPatch struct {
	Id         string `json:"id"`
	Attributes struct {
		AlarmRecords map[string]ackAttributes `json:"alarmRecords"`
	} `json:"attributes"`
}

// ackAttributes are the attributes of an alarm record that a consumer
// writes (the isWritable attributes of TS 28.111 7.3.1.2).
type ackAttributes struct {
	AckState    mns.AckState `json:"ackState"`
	AckUserId   string       `json:"ackUserId"`
	AckSystemId string       `json:"ackSystemId,omitempty"`
}

// acknowledge sets the ackState of the alarm records alarmIds, all of
// one node, with modifyMOIAttributes as a JSON Merge Patch (TS 28.532
// 12.1.1.1.4.2). ackUserId names the operator, and ackSystemId the
// OSS. Once the node has changed every record, the OAM answers 204, or
// 200 with the whole AlarmList as it is now (TS 32.158 6.3.2).
func (c consumer) acknowledge(ctx context.Context, alarmIds []string, ackState mns.AckState, ackUserId, ackSystemId string) error {
	patch := ackPatch{Id: alarmListId}
	patch.Attributes.AlarmRecords = map[string]ackAttributes{}
	for _, id := range alarmIds {
		patch.Attributes.AlarmRecords[id] = ackAttributes{AckState: ackState, AckUserId: ackUserId, AckSystemId: ackSystemId}
	}
	body, err := json.Marshal(patch)
	if err != nil {
		return err
	}
	resp, answer, err := c.do(ctx, http.MethodPatch, c.root.ResourceURI(c.alarmList()), mergePatch, body)
	if err != nil {
		return err
	}
	switch resp.StatusCode {
	case http.StatusNoContent:
		return nil
	case http.StatusOK:
		var list mns.AlarmListSingle
		if err := decodeAnswer(resp, answer, &list); err != nil {
			return err
		}
		if list.Id != alarmListId || list.Attributes == nil {
			return fmt.Errorf("PATCH %s: the answer is not the AlarmList %s: %s", resp.Request.URL, alarmListId, excerpt(answer))
		}
		return nil
	}
	return failed[mns.ErrorResponseDefault](resp, answer)
}

// do sends one request and reads the whole answer.
func (c consumer) do(ctx context.Context, method, uri, contentType string, body []byte) (*http.Response, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, uri, bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	answer, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswer+1))
	if err != nil {
		return nil, nil, fmt.Errorf("%s %s: %w", method, uri, err)
	}
	if len(answer) > maxAnswer {
		return nil, nil, fmt.Errorf("%s %s: %s with a body larger than the %d bytes oss reads", method, uri, resp.Status, maxAnswer)
	}
	return resp, answer, nil
}

// decodeAnswer decodes the JSON body of a successful answer strictly
// into v.
func decodeAnswer(resp *http.Response, answer []byte, v any) error {
	where := resp.Request.Method + " " + resp.Request.URL.String()
	if mediaType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type")); err != nil || mediaType != "application/json" {
		return fmt.Errorf("%s: %s with the Content-Type %q, not application/json", where, resp.Status, resp.Header.Get("Content-Type"))
	}
	if err := mns.DecodeStrictly(answer, v); err != nil {
		return fmt.Errorf("%s: %s: %v: %s", where, resp.Status, err, excerpt(answer))
	}
	return nil
}

// failed is the error for an answer with a status other than success:
// the status and the body, and whether the body is the error response
// T that TS 32.158 6.6.3 names for the request.
func failed[T any](resp *http.Response, answer []byte) error {
	err := fmt.Errorf("%s %s: %s: %s", resp.Request.Method, resp.Request.URL, resp.Status, excerpt(answer))
	var problem T
	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if mediaType != "application/json" {
		return fmt.Errorf("%w (the Content-Type is %q, not application/json)", err, resp.Header.Get("Content-Type"))
	}
	if problemErr := mns.DecodeStrictly(answer, &problem); problemErr != nil {
		return fmt.Errorf("%w (the body is no %T: %v)", err, problem, problemErr)
	}
	return err
}

// excerpt is the start of an answer, for an error message.
func excerpt(answer []byte) string {
	answer = bytes.TrimSpace(answer)
	if len(answer) > maxErrorAnswer {
		return fmt.Sprintf("%s... (%d bytes)", answer[:maxErrorAnswer], len(answer))
	}
	return string(answer)
}
