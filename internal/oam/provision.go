package oam

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/yuu-biz/ims-practice/internal/maintenance"
)

// DefaultProvisioningConcurrency is how many records of one bulk
// provisioning or restore the OAM sends to the HSS at once, when
// Config.ProvisioningConcurrency is 0.
const DefaultProvisioningConcurrency = 8

// maxRecordLine bounds one line of a bulk provisioning file, as the HSS
// bounds the body of one subscription.
const maxRecordLine = 64 << 10

// Result is what a record did, or would do on a dry run.
type Result string

const (
	Created   Result = "created"
	Updated   Result = "updated"
	Unchanged Result = "unchanged"
	Deleted   Result = "deleted"
	// Rejected is a record that the OAM or the HSS refused as it is:
	// sending it again changes nothing.
	Rejected Result = "rejected"
	// Failed is a record that the HSS did not take for a reason of its
	// own, such as no answer: sending it again may succeed.
	Failed Result = "failed"
)

// Outcome is the report on one record. Reason never holds a credential.
type Outcome struct {
	// Line is the number of the line in the file, from 1.
	Line            int    `json:"line,omitempty"`
	PrivateIdentity string `json:"private_identity,omitempty"`
	Result          Result `json:"result"`
	Reason          string `json:"reason,omitempty"`
	DryRun          bool   `json:"dry_run,omitempty"`

	// status is the HSS's status for a refused record, which the
	// single-record operations answer with.
	status int
}

// Summary counts the outcomes of a bulk provisioning or a restore.
type Summary struct {
	// Lines is how many lines were read, blank ones included; for a
	// restore, how many records.
	Lines     int  `json:"lines"`
	Created   int  `json:"created"`
	Updated   int  `json:"updated"`
	Unchanged int  `json:"unchanged"`
	Deleted   int  `json:"deleted"`
	Rejected  int  `json:"rejected"`
	Failed    int  `json:"failed"`
	DryRun    bool `json:"dry_run,omitempty"`
}

func (s *Summary) add(o Outcome) {
	switch o.Result {
	case Created:
		s.Created++
	case Updated:
		s.Updated++
	case Unchanged:
		s.Unchanged++
	case Deleted:
		s.Deleted++
	case Rejected:
		s.Rejected++
	default:
		s.Failed++
	}
}

// StreamLine is one line of the answer to an operation that streams:
// JSON objects, one per line, each with one of these fields. A bulk
// provisioning sends an outcome per record, then the summary. A backup
// sends each step as it starts, goes on and ends, then the manifest. A
// restore sends its steps, an outcome per record between them, then the
// summary. An error line says why the operation stopped early.
type StreamLine struct {
	Step     *Step     `json:"step,omitempty"`
	Outcome  *Outcome  `json:"outcome,omitempty"`
	Summary  *Summary  `json:"summary,omitempty"`
	Manifest *Manifest `json:"manifest,omitempty"`
	Error    string    `json:"error,omitempty"`
}

// stream writes StreamLines and sends each one at once.
type stream struct {
	enc *json.Encoder
	rc  *http.ResponseController
}

// startStream answers 200 with a stream of JSON objects. It lets the
// handler go on reading the request body while it writes, which
// HTTP/1.1 otherwise stops.
func startStream(w http.ResponseWriter) *stream {
	rc := http.NewResponseController(w)
	_ = rc.EnableFullDuplex()
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)
	return &stream{enc: json.NewEncoder(w), rc: rc}
}

func (s *stream) send(v any) error {
	if err := s.enc.Encode(v); err != nil {
		return err
	}
	return s.rc.Flush()
}

// errLineTooLong is a line longer than the buffer of its reader.
var errLineTooLong = errors.New("line too long")

// readLine reads one line without its end. A line longer than the
// buffer of r is skipped to its end and is errLineTooLong. io.EOF comes
// after the last line.
func readLine(r *bufio.Reader) ([]byte, error) {
	line, err := r.ReadSlice('\n')
	switch {
	case err == nil:
		return bytes.Clone(bytes.TrimRight(line, "\r\n")), nil
	case errors.Is(err, bufio.ErrBufferFull):
		for errors.Is(err, bufio.ErrBufferFull) {
			_, err = r.ReadSlice('\n')
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		return nil, errLineTooLong
	case errors.Is(err, io.EOF) && len(line) > 0:
		return bytes.Clone(bytes.TrimRight(line, "\r")), nil
	}
	return nil, err
}

// item is one record to provision: its line and its bytes, or why the
// line could not be read.
type item struct {
	line int
	raw  []byte
	err  error
	// section is the section of a record of a backup.
	section string
}

// lineSource gives the records of a file, one per non-blank line,
// without holding more than one line.
type lineSource struct {
	r     *bufio.Reader
	lines int
}

func newLineSource(r io.Reader) *lineSource {
	return &lineSource{r: bufio.NewReaderSize(r, maxRecordLine)}
}

func (l *lineSource) next() (item, error) {
	for {
		raw, err := readLine(l.r)
		if errors.Is(err, io.EOF) {
			return item{}, io.EOF
		}
		if errors.Is(err, errLineTooLong) {
			l.lines++
			return item{line: l.lines, err: fmt.Errorf("line longer than %d bytes", l.r.Size())}, nil
		}
		if err != nil {
			return item{}, err
		}
		l.lines++
		if len(bytes.TrimSpace(raw)) > 0 {
			return item{line: l.lines, raw: raw}, nil
		}
	}
}

// runOrdered calls work on each item that next gives, at most
// concurrency at a time, and emits the outcomes in the order of the
// items. At most concurrency items are in work and concurrency
// outcomes wait for emit, so a file of any length takes bounded memory.
// It stops when next gives io.EOF or an error, or when emit fails, and
// returns that error.
func runOrdered(ctx context.Context, concurrency int, next func() (item, error), work func(context.Context, item) Outcome, emit func(Outcome) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	queue := make(chan chan Outcome, concurrency)
	sem := make(chan struct{}, concurrency)
	var workers sync.WaitGroup
	var readErr error
	go func() {
		defer close(queue)
		for {
			it, err := next()
			if err != nil {
				if !errors.Is(err, io.EOF) {
					readErr = err
				}
				return
			}
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			result := make(chan Outcome, 1)
			select {
			case queue <- result:
			case <-ctx.Done():
				<-sem
				return
			}
			workers.Go(func() {
				defer func() { <-sem }()
				result <- work(ctx, it)
			})
		}
	}()
	var emitErr error
	for result := range queue {
		o := <-result
		if emitErr == nil {
			if emitErr = emit(o); emitErr != nil {
				cancel()
			}
		}
	}
	workers.Wait()
	if emitErr != nil {
		return emitErr
	}
	return readErr
}

// decodeRecord reads one record, refusing unknown fields. The error
// says where the record is wrong without quoting it, as the record may
// hold a credential.
func decodeRecord(raw []byte, rec *SubscriptionRecord) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	err := dec.Decode(rec)
	var syntax *json.SyntaxError
	var typ *json.UnmarshalTypeError
	switch {
	case errors.As(err, &syntax):
		return fmt.Errorf("not a JSON object: syntax error at offset %d", syntax.Offset)
	case errors.As(err, &typ):
		return fmt.Errorf("%s: want a JSON %s", typ.Field, typ.Type)
	case err != nil:
		return errors.New(strings.TrimPrefix(err.Error(), "json: "))
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("more than one JSON value on the line")
	}
	return nil
}

// redact replaces each secret in msg, so that a reason that the HSS
// gives cannot show a credential of the record even if it quotes it.
func redact(msg string, secrets []string) string {
	for _, s := range secrets {
		if s != "" {
			msg = strings.ReplaceAll(msg, s, "REDACTED")
		}
	}
	return msg
}

// provisionRaw parses, checks and provisions one record.
func (s *Server) provisionRaw(ctx context.Context, raw []byte, dryRun bool, o Outcome) Outcome {
	var rec SubscriptionRecord
	if err := decodeRecord(raw, &rec); err != nil {
		o.Result, o.Reason = Rejected, err.Error()
		return o
	}
	return s.provision(ctx, rec, dryRun, o)
}

// provision checks a record and creates, replaces or deletes its
// subscription in the HSS, or asks the HSS what it would do on a dry
// run.
func (s *Server) provision(ctx context.Context, rec SubscriptionRecord, dryRun bool, o Outcome) Outcome {
	o.PrivateIdentity, o.DryRun = rec.PrivateIdentity, dryRun
	if err := rec.validate(); err != nil {
		o.Result, o.Reason = Rejected, err.Error()
	} else {
		ctx, cancel := context.WithTimeout(ctx, s.nodeTimeout)
		defer cancel()
		if rec.Delete {
			o = s.deleteSubscription(ctx, rec.PrivateIdentity, dryRun, o)
		} else {
			a, err := s.hss.put(ctx, rec.hssRequest(), dryRun)
			switch {
			case err != nil:
				o = s.hssFailure(err, o)
			case a.Result == Created || a.Result == Updated || a.Result == Unchanged:
				o.Result = a.Result
			default:
				o.Result, o.Reason = Failed, fmt.Sprintf("the HSS answered the result %q", a.Result)
			}
		}
	}
	o.Reason = redact(o.Reason, rec.secrets())
	s.log.Debug("subscription provisioned", "private_identity", o.PrivateIdentity, "result", string(o.Result),
		"reason", o.Reason, "dry_run", dryRun)
	return o
}

func (s *Server) deleteSubscription(ctx context.Context, impi string, dryRun bool, o Outcome) Outcome {
	var err error
	if dryRun {
		_, err = s.hss.get(ctx, impi)
	} else {
		err = s.hss.delete(ctx, impi)
	}
	switch {
	case errors.Is(err, errNoSubscription):
		o.Result, o.Reason, o.status = Unchanged, "no such subscription", http.StatusNotFound
	case err != nil:
		o = s.hssFailure(err, o)
	default:
		o.Result = Deleted
	}
	return o
}

// hssFailure is the outcome of a record that the HSS did not take: one
// it refused as it is (400, 409, 422) is rejected, any other failure is
// failed.
func (s *Server) hssFailure(err error, o Outcome) Outcome {
	var refused *hssError
	o.Result, o.Reason, o.status = Failed, err.Error(), http.StatusBadGateway
	if errors.As(err, &refused) {
		switch refused.StatusCode {
		case http.StatusBadRequest, http.StatusConflict, http.StatusUnprocessableEntity:
			o.Result, o.Reason, o.status = Rejected, refused.Message, refused.StatusCode
		}
	}
	return o
}

// ProvisionStream provisions the records of r, one JSON object per
// line, and emits the outcome of each in the order of the lines. On a
// dry run the HSS changes nothing and says what it would do. Sending the
// same file again changes nothing: every record is then unchanged.
func (s *Server) ProvisionStream(ctx context.Context, r io.Reader, dryRun bool, emit func(Outcome) error) (Summary, error) {
	if s.hss == nil {
		return Summary{}, fmt.Errorf("%w: no HSS provisioning API", ErrUnavailable)
	}
	src := newLineSource(r)
	summary := Summary{DryRun: dryRun}
	err := runOrdered(ctx, s.provisioningConcurrency, src.next,
		func(ctx context.Context, it item) Outcome {
			o := Outcome{Line: it.line, DryRun: dryRun}
			if it.err != nil {
				o.Result, o.Reason = Rejected, it.err.Error()
				return o
			}
			return s.provisionRaw(ctx, it.raw, dryRun, o)
		},
		func(o Outcome) error {
			summary.add(o)
			return emit(o)
		})
	summary.Lines = src.lines
	s.log.Info("bulk provisioning done", "summary", summary)
	return summary, err
}

func (s *Server) subscriptionRoutes(rt *routes) {
	rt.handle(http.MethodPost, PathPrefix+"provisioning", s.withHSS(func(w http.ResponseWriter, r *http.Request) {
		dry, err := dryRun(r)
		if err != nil {
			writeError(w, err)
			return
		}
		out := startStream(w)
		summary, err := s.ProvisionStream(r.Context(), r.Body, dry, func(o Outcome) error {
			return out.send(StreamLine{Outcome: &o})
		})
		if err != nil {
			_ = out.send(StreamLine{Error: err.Error()})
		}
		_ = out.send(StreamLine{Summary: &summary})
	}))
	rt.handle(http.MethodGet, PathPrefix+"subscriptions/{impi}", s.withHSS(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), s.nodeTimeout)
		defer cancel()
		sub, err := s.hss.get(ctx, r.PathValue("impi"))
		if err != nil {
			writeError(w, hssLookupError(err))
			return
		}
		writeJSON(w, http.StatusOK, sub)
	}))
	rt.handle(http.MethodPut, PathPrefix+"subscriptions/{impi}", s.withHSS(func(w http.ResponseWriter, r *http.Request) {
		dry, err := dryRun(r)
		var rec SubscriptionRecord
		if err == nil {
			err = readRecord(w, r, &rec)
		}
		if err != nil {
			writeError(w, err)
			return
		}
		impi := r.PathValue("impi")
		switch {
		case rec.PrivateIdentity == "":
			rec.PrivateIdentity = impi
		case !strings.EqualFold(rec.PrivateIdentity, impi):
			writeError(w, fmt.Errorf("%w: private_identity is not the one of the path", ErrInvalidRequest))
			return
		}
		if rec.Delete {
			writeError(w, fmt.Errorf("%w: delete: use DELETE", ErrInvalidRequest))
			return
		}
		writeOutcome(w, s.provision(r.Context(), rec, dry, Outcome{}))
	}))
	rt.handle(http.MethodDelete, PathPrefix+"subscriptions/{impi}", s.withHSS(func(w http.ResponseWriter, r *http.Request) {
		dry, err := dryRun(r)
		if err != nil {
			writeError(w, err)
			return
		}
		rec := SubscriptionRecord{PrivateIdentity: r.PathValue("impi"), Delete: true}
		writeOutcome(w, s.provision(r.Context(), rec, dry, Outcome{}))
	}))
}

// readRecord reads the body of a single-record operation as a line of a
// bulk file is read, so that an error never quotes the record.
func readRecord(w http.ResponseWriter, r *http.Request, rec *SubscriptionRecord) error {
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRecordLine))
	if err != nil {
		return fmt.Errorf("%w: body: %v", ErrInvalidRequest, err)
	}
	if err := decodeRecord(b, rec); err != nil {
		return fmt.Errorf("%w: body: %v", ErrInvalidRequest, err)
	}
	return nil
}

// writeOutcome answers a single-record operation: the Outcome when the
// record went through (201 when it created the subscription), an
// ErrorBody with the HSS's status when it did not.
func writeOutcome(w http.ResponseWriter, o Outcome) {
	switch o.Result {
	case Created:
		status := http.StatusCreated
		if o.DryRun {
			status = http.StatusOK
		}
		writeJSON(w, status, o)
	case Updated, Deleted:
		writeJSON(w, http.StatusOK, o)
	case Unchanged:
		if o.status == http.StatusNotFound {
			writeError(w, fmt.Errorf("%w: %s", ErrNotFound, o.Reason))
			return
		}
		writeJSON(w, http.StatusOK, o)
	default:
		status := o.status
		if status == 0 {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, maintenance.ErrorBody{Error: o.Reason})
	}
}

// hssLookupError is the error of the OAM for an error of the HSS.
func hssLookupError(err error) error {
	if errors.Is(err, errNoSubscription) {
		return fmt.Errorf("%w: no such subscription", ErrNotFound)
	}
	if errors.Is(err, ErrUpstream) {
		return err
	}
	return fmt.Errorf("%w: %v", ErrUpstream, err)
}

// withHSS answers 503 when the OAM has no HSS provisioning API.
func (s *Server) withHSS(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.hss == nil {
			writeError(w, fmt.Errorf("%w: no HSS provisioning API", ErrUnavailable))
			return
		}
		h(w, r)
	}
}
