package oam

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/yuu-biz/ims-practice/internal/maintenance"
)

// Alarms is the answer to GET /oam/v1/alarms: the alarm list of every
// node, merged. It has the shape of a FanOut, with one entry for every
// node of the inventory, and the records of the nodes that answered in
// Alarms, the most severe first, then the newest first.
//
// A node that does not answer is a failed entry, as in every FanOut;
// the OAM makes up no alarm for it. TS 28.111 6.11 has a manager that
// loses a managed element mark that part of its alarm list as
// unreliable, not raise an alarm of its own, and an alarm of the
// standard is detected by the alarmed system (6.14). The roles that
// talk to the node raise the alarms of the link to it, such as an
// S-CSCF whose HSS stops answering, and the failed entry says that the
// node's own alarms are unknown.
type Alarms struct {
	Succeeded int                         `json:"succeeded"`
	Failed    int                         `json:"failed"`
	Nodes     []NodeResult[AlarmListInfo] `json:"nodes"`
	Alarms    []NodeAlarmRecord           `json:"alarms"`
}

// AlarmListInfo is what a node said about its alarm list, apart from the
// records, which are in Alarms.Alarms.
type AlarmListInfo struct {
	Instance string `json:"instance,omitempty"`
	// NumOfAlarmRecords counts the records in the node's list, whether
	// the filter kept them or not.
	NumOfAlarmRecords int `json:"num_of_alarm_records"`
	// Listed counts the records of the node that the filter kept.
	Listed           int       `json:"listed"`
	LastModification time.Time `json:"last_modification"`
	// DroppedRecords counts the records that the node removed because
	// its list was full.
	DroppedRecords uint64 `json:"dropped_records"`
}

// NodeAlarmRecord is an alarm record with the node of the inventory
// whose list holds it.
type NodeAlarmRecord struct {
	Node string `json:"node"`
	maintenance.AlarmRecord
}

// Alarms reads the alarm list of each target with the filter, and
// merges the records.
func (s *Server) Alarms(ctx context.Context, targets []Endpoint, f maintenance.AlarmFilter) Alarms {
	lists := fanOut(ctx, s, targets, func(ctx context.Context, c *maintenance.Client, _ Endpoint) (maintenance.AlarmList, error) {
		return readAlarmList(ctx, c, f)
	})
	a := Alarms{Succeeded: lists.Succeeded, Failed: lists.Failed, Nodes: make([]NodeResult[AlarmListInfo], len(lists.Nodes)), Alarms: []NodeAlarmRecord{}}
	order := map[string]int{}
	for i, n := range lists.Nodes {
		order[n.Node] = i
		a.Nodes[i] = NodeResult[AlarmListInfo]{Node: n.Node, URL: n.URL, Error: n.Error}
		if n.Result == nil {
			continue
		}
		l := n.Result
		a.Nodes[i].Result = &AlarmListInfo{
			Instance:          l.Instance,
			NumOfAlarmRecords: l.NumOfAlarmRecords,
			Listed:            len(l.AlarmRecords),
			LastModification:  l.LastModification,
			DroppedRecords:    l.DroppedRecords,
		}
		for _, r := range l.AlarmRecords {
			a.Alarms = append(a.Alarms, NodeAlarmRecord{Node: n.Node, AlarmRecord: r})
		}
	}
	slices.SortFunc(a.Alarms, func(x, y NodeAlarmRecord) int {
		return cmp.Or(
			maintenance.CompareAlarmRecords(x.AlarmRecord, y.AlarmRecord),
			cmp.Compare(order[x.Node], order[y.Node]),
			cmp.Compare(x.AlarmID, y.AlarmID),
		)
	})
	return a
}

// readAlarmList reads the alarm list of a node with the filter.
func readAlarmList(ctx context.Context, c *maintenance.Client, f maintenance.AlarmFilter) (maintenance.AlarmList, error) {
	l, err := c.Alarms(ctx, f)
	if err != nil {
		return l, err
	}
	// The node checks each alarm when it is raised; a node of another
	// build might not, and the OAM passes on nothing that may hold a
	// secret or name a subscriber.
	for _, r := range l.AlarmRecords {
		if err := maintenance.CheckAlarmRecord(r); err != nil {
			return maintenance.AlarmList{}, fmt.Errorf("the node's alarm list: %w", err)
		}
	}
	return l, nil
}

// SetAckState acknowledges an alarm of one node, or takes the
// acknowledgement back (TS 28.111 6.7).
func (s *Server) SetAckState(ctx context.Context, target Endpoint, alarmID string, req maintenance.AckStateRequest) FanOut[maintenance.AlarmRecord] {
	s.log.Info("alarm acknowledgement requested", "node", target.Name, "alarm_id", alarmID,
		"ack_state", string(req.AckState), "operator", req.Operator)
	return fanOut(ctx, s, []Endpoint{target}, func(ctx context.Context, c *maintenance.Client, _ Endpoint) (maintenance.AlarmRecord, error) {
		return c.SetAckState(ctx, alarmID, req)
	})
}

func (s *Server) alarmRoutes(rt *routes) {
	rt.handle(http.MethodGet, PathPrefix+"alarms", func(w http.ResponseWriter, r *http.Request) {
		// The filter is checked here, so that a bad one is one 400 and
		// not a 400 from every node.
		f, err := maintenance.ParseAlarmFilter(r.URL.Query())
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, s.Alarms(r.Context(), s.nodes, f))
	})
	rt.handle(http.MethodPut, PathPrefix+"nodes/{node}/alarms/{id}/ack-state", s.withNode(func(w http.ResponseWriter, r *http.Request, ts []Endpoint) {
		var req maintenance.AckStateRequest
		if err := readJSON(w, r, &req, false); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, s.SetAckState(r.Context(), ts[0], r.PathValue("id"), req))
	}))
}
