package xcap

import (
	"bytes"
	"context"
	"errors"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/sh"
	"github.com/yuu-biz/ims-practice/internal/simservs"
)

// Repository is where the server reads and writes what it serves: the
// Sh Repository Data of the HSS. *sh.Client is one. The server
// keeps no copy, so every request reads it again.
type Repository interface {
	RepositoryData(ctx context.Context, publicID, serviceIndication string) (data sh.TransparentData, found bool, err error)
	UpdateRepositoryData(ctx context.Context, publicID, serviceIndication string, read uint16, serviceData []byte) error
}

// maxBody bounds the body of a PUT. It is the most ServiceData the HSS
// stub takes; a document that large could not be stored anyway.
const maxBody = 64 << 10

// maxAttempts bounds how often an unconditional change is read and
// written again when another writer changed the document in between
// (5105 from the HSS).
const maxAttempts = 3

// Handler is the XCAP server of the simservs application usage (TS
// 24.623 6.2). Its documents are at
//
//	<XCAP Root>/simservs.ngn.etsi.org/users/<XUI>/simservs.xml
//
// and the XCAP server capabilities at <XCAP Root>/xcap-caps/global/index
// (RFC 4825 12).
type Handler struct {
	repo   Repository
	root   string // the path of the XCAP Root URI, without a trailing slash
	realm  string // the realm of every challenge
	nonces nonces
	log    *slog.Logger
}

// NewHandler returns the server for the documents in repo. rootPath is
// the path of the XCAP Root URI: empty for http://xcap.<domain>, the
// form of TS 23.003 13.9.1.2. realm is the realm of the Digest
// challenges: the home network domain, in which the HSS derives the Ut
// credential of every user (hss.UtCredentialData). nonceKey signs the
// Digest nonces; every instance of the server must have the same one,
// so that each takes the nonces of the others.
func NewHandler(repo Repository, rootPath, realm string, nonceKey []byte, log *slog.Logger) *Handler {
	return &Handler{
		repo:   repo,
		root:   strings.TrimSuffix(rootPath, "/"),
		realm:  realm,
		nonces: nonces{key: nonceKey, now: timeNow},
		log:    log,
	}
}

// target is what the request URI names (RFC 4825 6): the document of
// the user xui, and in it the node of selector, or the whole document
// when selector is nil.
type target struct {
	xui      string
	selector *simservs.NodeSelector
	// selectorErr is ErrUndefinedPrefix or another error of the
	// node selector that is answered with 400 once the request is
	// authorized (RFC 4825 8).
	selectorErr error
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// RFC 4825 9: the documents are written by clients, and caches do
	// not know that a change to one element changes the others.
	w.Header().Set("Cache-Control", "no-cache")
	rest, ok := strings.CutPrefix(r.URL.EscapedPath(), h.root+"/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	if rest == capsDocumentSelector {
		serveCaps(w, r)
		return
	}
	t, ok := parseTarget(rest, r.URL.RawQuery)
	if !ok {
		// An AUID, a tree or a document that this server does not have,
		// or an extension selector it does not understand (RFC 4825 8).
		h.answer(w, r, t, http.StatusNotFound, "no such resource")
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete:
	default:
		// RFC 4825 8.1: POST does not apply to XCAP resources.
		//
		// SIMPLIFIED: TS 24.623 5.3.1.3 and 5.3.2.5. The password check
		// and the password change that a UE asks for with a POST are not
		// supported, nor is the password control of a service. GSMA
		// IR.92 2.3.9 lets the network leave out 5.3.2.5, and Digest
		// already authenticates the user. checkPasswordControl keeps a
		// service that the operator put under password control from
		// being changed at all.
		w.Header().Set("Allow", "GET, PUT, DELETE")
		h.answer(w, r, t, http.StatusMethodNotAllowed, "method not allowed on an XCAP resource")
		return
	}
	if !h.authorize(w, r, t) {
		return
	}
	if t.selectorErr != nil {
		h.answer(w, r, t, http.StatusBadRequest, t.selectorErr.Error())
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		h.get(w, r, t)
	case http.MethodPut:
		h.put(w, r, t)
	case http.MethodDelete:
		h.delete(w, r, t)
	}
}

// parseTarget reads the document selector and the node selector of an
// XCAP URI (RFC 4825 6.2, 6.3): the escaped path after the XCAP root,
// and the query component that binds the prefixes of the node selector
// (6.4). ok is false for a URI that names nothing of this server.
func parseTarget(path, rawQuery string) (target, bool) {
	// RFC 4825 6: %7E%7E is the separator too.
	path = strings.NewReplacer("%7E%7E", "~~", "%7e%7e", "~~", "%7E%7e", "~~", "%7e%7E", "~~").Replace(path)
	document, node, hasNode := strings.Cut(path, "/~~/")
	segments := strings.Split(document, "/")
	if len(segments) != 4 {
		return target{}, false
	}
	for i, s := range segments {
		u, err := url.PathUnescape(s)
		if err != nil {
			return target{}, false
		}
		segments[i] = u
	}
	// Every simservs document is in a user's home directory; the
	// application usage has no global documents (TS 24.623 6.2).
	if segments[0] != simservs.AUID || segments[1] != "users" || segments[3] != simservs.DocumentName || segments[2] == "" {
		return target{}, false
	}
	t := target{xui: segments[2]}
	if !hasNode {
		return t, true
	}
	selector, err1 := url.PathUnescape(node)
	query, err2 := url.PathUnescape(rawQuery)
	if err := errors.Join(err1, err2); err != nil {
		t.selectorErr = err
		return t, true
	}
	t.selector, t.selectorErr = simservs.ParseNodeSelector(selector, query, simservs.NamespaceSimservs)
	if errors.Is(t.selectorErr, simservs.ErrUnsupportedSelector) {
		return t, false
	}
	return t, true
}

// authorize authenticates the request and checks that it comes from the
// owner of the document, the only user allowed any operation on it (TS
// 24.623 5.3.2.2, 6.2). It has answered the request when it returns
// false.
//
// The server authenticates first and authorizes after (TS 24.623
// 5.3.2.2). A request without Digest credentials gets a 401 at once
// (5.2.3.2.1 a), before the server reads anything of the user, with a
// challenge that is the same for every user. One whose credentials the
// server cannot check gets the same 401 as one with a wrong password:
// when the XUI names no user, a user without a Ut credential, or a user
// whose username is not the one in the credentials. Only the owner of
// the document has a credential that the server can find, since Sh
// finds Repository Data by public identity alone. So the answers tell
// no one who cannot authenticate which users there are, or which of
// them use Ut.
//
// The challenge that answers credentials which did not authenticate is
// built from the credential of the XUI, when there is one: its realm and
// its algorithms. The HSS derives every credential in the home domain
// for both algorithms, which makes that challenge the same as the first.
// A credential that the operator provisioned as it is may have another
// realm or H(A1) for MD5 alone, and its user then authenticates with
// the second challenge (RFC 7616 3.3). That challenge tells of such a
// credential, to a client that has sent credentials for the XUI.
//
// A user is allowed to use Ut by having a Ut credential, and one
// without cannot authenticate. The 403 of TS 24.623 5.3.2.3, to an
// authenticated user who is not allowed to use Ut, has no case here.
//
// SIMPLIFIED: TS 24.623 5.2.3.2.1. A user who asks for the document of
// another user, with the right password of their own, gets 401, where
// 5.2.3.2.1 b has the AS authenticate them and then deny the
// authorization, with 403. The server can authenticate only the owner
// of the document: Sh finds the Ut credential by the public identity of
// the XUI, and there is no request that finds the credential of the
// username a client gives. The answer is still a refusal, and the
// owner's settings stay the owner's.
func (h *Handler) authorize(w http.ResponseWriter, r *http.Request, t target) bool {
	var cred Credential // none, which no credentials match
	if hasDigest(r) {
		found, ok := h.credential(w, r, t)
		if !ok {
			return false
		}
		cred = found
	}
	v, info := h.nonces.verify(r, cred)
	switch v {
	case authenticated:
		w.Header().Set("Authentication-Info", info)
		return true
	case unauthenticated, staleNonce:
		if hasDigest(r) && v == unauthenticated {
			// RFC 7616 3.4: a failed login is logged, as repeated ones
			// may be an attack.
			h.log.Warn("Ut login failed", "xui", t.xui)
		}
		realm, offered := h.realm, algorithms
		if cred.Username != "" {
			realm, offered = cred.Realm, cred.algorithms()
		}
		h.nonces.challenge(w.Header(), realm, offered, v == staleNonce)
		h.answer(w, r, t, http.StatusUnauthorized, "authentication required")
	default:
		h.answer(w, r, t, http.StatusBadRequest, "malformed Authorization")
	}
	return false
}

// credential reads the Ut credential of the user of the XUI. It is the
// zero Credential when the HSS has no such user, or the user has no
// credential. When it returns false for ok, it has answered the
// request.
func (h *Handler) credential(w http.ResponseWriter, r *http.Request, t target) (cred Credential, ok bool) {
	stored, found, err := h.repo.RepositoryData(r.Context(), t.xui, CredentialServiceIndication)
	switch {
	case isResult(err, diameter.Experimental(cx.DiameterErrorUserUnknown)):
		return Credential{}, true
	case err != nil:
		h.repositoryFailed(w, r, t, err)
		return Credential{}, false
	case !found:
		return Credential{}, true
	}
	cred, err = ParseCredential(stored.ServiceData.XML)
	if err != nil {
		h.answer(w, r, t, http.StatusInternalServerError, err.Error())
		return Credential{}, false
	}
	return cred, true
}

// userDocument reads the user's MMTelServices document from the HSS.
// found is false when the HSS has none, which means the user has no
// simservs document. When it returns false for ok, it has answered the
// request.
func (h *Handler) userDocument(w http.ResponseWriter, r *http.Request, t target) (stored sh.TransparentData, mmtel *simservs.Document, found, ok bool) {
	stored, found, err := h.repo.RepositoryData(r.Context(), t.xui, simservs.ServiceIndication)
	if err != nil {
		h.repositoryFailed(w, r, t, err)
		return sh.TransparentData{}, nil, false, false
	}
	if !found {
		return stored, nil, false, true
	}
	if mmtel, err = simservs.ParseDocument(stored.ServiceData.XML); err != nil {
		h.answer(w, r, t, http.StatusInternalServerError, "the stored MMTelServices document is broken: "+err.Error())
		return sh.TransparentData{}, nil, false, false
	}
	return stored, mmtel, true, true
}

// entityTag is the entity tag of a document: the Sequence Number of
// the Repository Data that holds it, which changes with every write
// (TS 29.328 6.1.2.1). One tag covers the whole document (RFC 4825
// 8.5).
func entityTag(sequenceNumber uint16) string {
	return `"` + strconv.Itoa(int(sequenceNumber)) + `"`
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request, t target) {
	stored, mmtel, found, ok := h.userDocument(w, r, t)
	if !ok {
		return
	}
	if !found {
		// RFC 4825 8.3. GSMA IR.92 2.3.2 has the UE try another XUI.
		h.answer(w, r, t, http.StatusNotFound, "the user has no simservs document")
		return
	}
	etag := entityTag(stored.SequenceNumber)
	w.Header().Set("ETag", etag)
	if status := precondition(r, etag, true); status != 0 {
		h.answer(w, r, t, status, "")
		return
	}
	user, err := simservs.UserDocument(mmtel)
	if err != nil {
		h.fail(w, r, t, err)
		return
	}
	body, contentType := user.Bytes(), simservs.MIMEType
	if t.selector != nil {
		if body, contentType, err = user.Get(t.selector); err != nil {
			h.fail(w, r, t, err)
			return
		}
	}
	w.Header().Set("Content-Type", contentType)
	h.log.Info("XCAP", "method", r.Method, "xui", t.xui, "selector", selectorText(t), "status", http.StatusOK, "etag", etag)
	w.WriteHeader(http.StatusOK)
	w.Write(body)
}

func (h *Handler) put(w http.ResponseWriter, r *http.Request, t target) {
	want := simservs.MIMEType
	switch {
	case t.selector == nil:
	case t.selector.TargetsNamespaces():
		h.fail(w, r, t, simservs.ErrNamespaceSelector)
		return
	case t.selector.TargetsAttribute():
		want = simservs.AttributeMIMEType
	default:
		want = simservs.ElementMIMEType
	}
	// RFC 4825 8.2.2.
	if got, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); got != want {
		h.answer(w, r, t, http.StatusUnsupportedMediaType, "the body must be "+want)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		h.answer(w, r, t, http.StatusRequestEntityTooLarge, err.Error())
		return
	}
	h.change(w, r, t, func(before *simservs.Document) (*simservs.Document, bool, error) {
		if t.selector == nil {
			// RFC 4825 8.2.4: the body replaces the document.
			after, err := simservs.ParseDocument(body)
			return after, false, err
		}
		after := before.Clone()
		var created bool
		var err error
		if t.selector.TargetsAttribute() {
			created, err = after.PutAttribute(t.selector, body)
		} else {
			created, err = after.PutElement(t.selector, body)
		}
		return after, created, err
	})
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request, t target) {
	if t.selector != nil && t.selector.TargetsNamespaces() {
		h.fail(w, r, t, simservs.ErrNamespaceSelector)
		return
	}
	h.change(w, r, t, func(before *simservs.Document) (*simservs.Document, bool, error) {
		if t.selector == nil {
			// TS 24.623 6.2 c: the owner may not remove the services,
			// and deleting the document would remove them all.
			return nil, false, &simservs.ConflictError{Condition: simservs.CannotDelete, Phrase: "the services of the document cannot be removed"}
		}
		after := before.Clone()
		return after, false, after.Delete(t.selector)
	})
}

// edit changes the user's simservs document, before, into the one the
// request asks for. created tells whether the node it put is new.
type edit func(before *simservs.Document) (after *simservs.Document, created bool, err error)

// change is the read-modify-write of a PUT or DELETE: read the
// MMTelServices document, check the conditions of the request against
// its entity tag, apply the edit to its user parts, check the result
// (RFC 4825 8.2.5, TS 24.623 5.3.2.4 and 6.2), and write it back with
// the Sequence Number it was read with.
//
// When the HSS answers 5105 because another writer got in between, a
// conditional request fails with 412, as it would have had it come
// after that writer; an unconditional one starts again from the newer
// document.
func (h *Handler) change(w http.ResponseWriter, r *http.Request, t target, apply edit) {
	conditional := r.Header.Get("If-Match") != "" || r.Header.Get("If-None-Match") != ""
	for attempt := 1; ; attempt++ {
		stored, mmtel, found, ok := h.userDocument(w, r, t)
		if !ok {
			return
		}
		if !found {
			h.absent(w, r, t)
			return
		}
		etag := entityTag(stored.SequenceNumber)
		if status := precondition(r, etag, true); status != 0 {
			w.Header().Set("ETag", etag)
			h.answer(w, r, t, status, "")
			return
		}
		before, err := simservs.UserDocument(mmtel)
		if err != nil {
			h.fail(w, r, t, err)
			return
		}
		after, created, err := apply(before)
		if err == nil {
			err = simservs.ValidateSimservsDocument(after)
		}
		if err == nil {
			err = simservs.CheckOwnerChange(before, after)
		}
		if err == nil {
			err = checkPasswordControl(before, after)
		}
		next := mmtel.Clone()
		if err == nil {
			err = simservs.SetUserDocument(next, after)
		}
		if err != nil {
			h.fail(w, r, t, err)
			return
		}
		err = h.repo.UpdateRepositoryData(r.Context(), t.xui, simservs.ServiceIndication, stored.SequenceNumber, next.RootElement())
		if isResult(err, diameter.Experimental(sh.DiameterErrorTransparentDataOutOfSync)) {
			if conditional {
				h.answer(w, r, t, http.StatusPreconditionFailed, "the document changed meanwhile")
				return
			}
			if attempt < maxAttempts {
				continue
			}
			w.Header().Set("Retry-After", "1")
			h.answer(w, r, t, http.StatusServiceUnavailable, "the document keeps changing")
			return
		}
		if err != nil {
			h.repositoryFailed(w, r, t, err)
			return
		}
		etag = entityTag(sh.NextSequenceNumber(stored.SequenceNumber))
		w.Header().Set("ETag", etag)
		status := http.StatusOK
		if created {
			status = http.StatusCreated
		}
		// RFC 4825 8.2.7: a 200 to PUT has no body.
		h.log.Info("XCAP", "method", r.Method, "xui", t.xui, "selector", selectorText(t), "status", status, "etag", etag)
		w.WriteHeader(status)
		return
	}
}

// absent answers a PUT or DELETE for a user who has no simservs
// document. The document comes from the operator's MMTelServices, so
// a user cannot create one (TS 24.623 6.2 b).
func (h *Handler) absent(w http.ResponseWriter, r *http.Request, t target) {
	if status := precondition(r, "", false); status != 0 {
		h.answer(w, r, t, status, "")
		return
	}
	switch {
	case r.Method == http.MethodDelete:
		h.answer(w, r, t, http.StatusNotFound, "the user has no simservs document")
	case t.selector != nil:
		// RFC 4825 8.2.1.
		h.fail(w, r, t, &simservs.ConflictError{Condition: simservs.NoParent, Phrase: "the user has no simservs document"})
	default:
		// TS 24.623 5.3.2.4.
		h.fail(w, r, t, &simservs.ConflictError{Condition: simservs.ConstraintFailure, Phrase: "no supplementary service is provisioned for the user"})
	}
}

// checkPasswordControl refuses any change to a barring service that
// the operator put under password control (the password-required
// attribute of TS 24.611 4.9.2), as the password check of TS 24.623
// 5.3.2.5.2 is not supported (see the SIMPLIFIED in ServeHTTP). Without
// it the user could change the service, or turn the control off,
// without the password.
func checkPasswordControl(before, after *simservs.Document) error {
	b, err := simservs.ParseSimservs(before.Bytes())
	if err != nil {
		return nil // ValidateSimservsDocument has read both already
	}
	controlled := map[string]bool{
		"incoming-communication-barring": b.IncomingCommunicationBarring != nil && isTrue(b.IncomingCommunicationBarring.PasswordRequired),
		"outgoing-communication-barring": b.OutgoingCommunicationBarring != nil && isTrue(b.OutgoingCommunicationBarring.PasswordRequired),
	}
	for name, ok := range controlled {
		if !ok {
			continue
		}
		sel, err := simservs.ParseNodeSelector("simservs/"+name, "", simservs.NamespaceSimservs)
		if err != nil {
			panic(err) // a constant selector
		}
		was, _, _ := before.Get(sel)
		is, _, _ := after.Get(sel)
		if !bytes.Equal(was, is) {
			return &simservs.ConflictError{Condition: simservs.ConstraintFailure, Phrase: "<" + name + "> needs a password, which this server does not check"}
		}
	}
	return nil
}

func isTrue(b *bool) bool { return b != nil && *b }

// precondition evaluates If-Match and If-None-Match against the entity
// tag of the document, or against no document when exists is false
// (RFC 9110 13.2.2, RFC 4825 7.11). It returns the status to answer
// with, or 0 to go on.
func precondition(r *http.Request, current string, exists bool) int {
	if list := strings.Join(r.Header.Values("If-Match"), ","); list != "" {
		if !matches(list, current, exists, false) {
			return http.StatusPreconditionFailed
		}
	}
	if list := strings.Join(r.Header.Values("If-None-Match"), ","); list != "" {
		if matches(list, current, exists, true) {
			if r.Method == http.MethodGet || r.Method == http.MethodHead {
				return http.StatusNotModified
			}
			return http.StatusPreconditionFailed
		}
	}
	return 0
}

// matches tells whether the entity tags in list match current, with the
// weak comparison of If-None-Match or the strong one of If-Match (RFC
// 9110 8.8.3.2). "*" matches any document that exists.
func matches(list, current string, exists, weak bool) bool {
	if strings.TrimSpace(list) == "*" {
		return exists
	}
	if !exists {
		return false
	}
	for _, tag := range strings.Split(list, ",") {
		tag = strings.TrimSpace(tag)
		if strings.HasPrefix(tag, "W/") {
			if !weak {
				continue
			}
			tag = tag[2:]
		}
		if tag == current {
			return true
		}
	}
	return false
}

// fail answers with the status that RFC 4825 8 gives an error of
// package simservs: 409 with the conflict report for a *ConflictError.
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, t target, err error) {
	var conflict *simservs.ConflictError
	switch {
	case errors.As(err, &conflict):
		h.log.Info("XCAP", "method", r.Method, "xui", t.xui, "selector", selectorText(t), "status", http.StatusConflict,
			"condition", string(conflict.Condition), "phrase", conflict.Phrase)
		w.Header().Set("Content-Type", simservs.ErrorMIMEType)
		w.WriteHeader(http.StatusConflict)
		w.Write(conflict.Report())
	case errors.Is(err, simservs.ErrNoMatch):
		h.answer(w, r, t, http.StatusNotFound, err.Error())
	case errors.Is(err, simservs.ErrNamespaceSelector):
		// RFC 4825 8.2, 8.4.
		w.Header().Set("Allow", "GET")
		h.answer(w, r, t, http.StatusMethodNotAllowed, err.Error())
	default:
		h.answer(w, r, t, http.StatusInternalServerError, err.Error())
	}
}

// repositoryFailed answers a request whose UDR or PUR failed: 404 for
// a user the HSS does not know (RFC 4825 8), 503 when the HSS could not
// be asked or did not comply.
func (h *Handler) repositoryFailed(w http.ResponseWriter, r *http.Request, t target, err error) {
	if isResult(err, diameter.Experimental(cx.DiameterErrorUserUnknown)) {
		h.answer(w, r, t, http.StatusNotFound, "no such user")
		return
	}
	h.log.Warn("HSS request failed", "xui", t.xui, "error", err.Error())
	h.answer(w, r, t, http.StatusServiceUnavailable, "the HSS did not answer")
}

func isResult(err error, result diameter.Result) bool {
	var re *sh.ResultError
	return errors.As(err, &re) && re.Result == result
}

// answer ends a request with status and, for an error, reason as text.
func (h *Handler) answer(w http.ResponseWriter, r *http.Request, t target, status int, reason string) {
	h.log.Info("XCAP", "method", r.Method, "xui", t.xui, "selector", selectorText(t), "status", status, "reason", reason)
	if status == http.StatusNotModified || reason == "" {
		w.WriteHeader(status)
		return
	}
	http.Error(w, reason, status)
}

func selectorText(t target) string {
	if t.selector == nil {
		return ""
	}
	return t.selector.String()
}
