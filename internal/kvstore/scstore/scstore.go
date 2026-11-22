// Package scstore is an sc.Store on a kvstore.Store, so that the SC stub
// of whichever IP-SM-GW instance hears the outcome of a delivery finds
// the status report request that another one stored.
//
// One kind of item, after the prefix of the Config and "sc:":
//
//	status-report-request:<reference>   StatusReportRequest, time to live until its Expires
package scstore

import (
	"context"
	"errors"

	"github.com/yuu-biz/ims-practice/internal/kvstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/sc"
)

// Store is an sc.Store on a kvstore.Store.
type Store struct {
	c *kvjson.Client
}

var _ sc.Store = (*Store)(nil)

// New returns a Store on kv. Instances with the same kv and Prefix share
// their state.
func New(kv kvstore.Store, cfg kvjson.Config) *Store {
	return &Store{c: kvjson.New(kv, cfg, "sc")}
}

func (s *Store) PutStatusReportRequest(ctx context.Context, r sc.StatusReportRequest) error {
	return kvjson.Write(ctx, s.c, s.c.Key("status-report-request", r.Reference), r, s.c.TTLUntil(r.Expires))
}

// TakeStatusReportRequest is a read and a compare and delete
// (kvjson.ReadAndRemove): of two outcomes of one delivery that reach two
// instances, one writes the report. A request past its Expires that the
// store still keeps, by its own clock, is none.
func (s *Store) TakeStatusReportRequest(ctx context.Context, reference string) (sc.StatusReportRequest, bool, error) {
	r, err := kvjson.ReadAndRemove[sc.StatusReportRequest](ctx, s.c, s.c.Key("status-report-request", reference))
	switch {
	case errors.Is(err, kvstore.ErrNotFound):
		return sc.StatusReportRequest{}, false, nil
	case err != nil:
		return sc.StatusReportRequest{}, false, err
	case !s.c.Now().Before(r.Expires):
		return sc.StatusReportRequest{}, false, nil
	}
	return r, true, nil
}
