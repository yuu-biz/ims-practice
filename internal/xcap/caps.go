package xcap

import (
	"net/http"

	"github.com/yuu-biz/ims-practice/internal/simservs"
)

// capsDocumentSelector is the document of the XCAP server capabilities
// application usage, which every XCAP server supports (RFC 4825 12). A
// client reads it to learn which application usages and namespaces the
// server knows.
const capsDocumentSelector = "xcap-caps/global/index"

// capsMIMEType is the MIME type of the capabilities document (RFC 4825
// 12.4).
const capsMIMEType = "application/xcap-caps+xml"

// capsDocument lists the application usages of this server and the
// namespaces whose schemas it validates (RFC 4825 12). It names no
// extensions: the server knows only the selectors of RFC 4825 6.3.
var capsDocument = []byte(`<?xml version="1.0" encoding="UTF-8"?>
<xcap-caps xmlns="urn:ietf:params:xml:ns:xcap-caps">
  <auids>
    <auid>xcap-caps</auid>
    <auid>` + simservs.AUID + `</auid>
  </auids>
  <extensions/>
  <namespaces>
    <namespace>urn:ietf:params:xml:ns:xcap-caps</namespace>
    <namespace>` + simservs.NamespaceSimservs + `</namespace>
    <namespace>` + simservs.NamespaceCommonPolicy + `</namespace>
  </namespaces>
</xcap-caps>
`)

// capsETag never changes: the document is fixed for a build.
const capsETag = `"xcap-caps-1"`

// serveCaps answers a request for the capabilities document. It is a
// global document, which any user may read and none may change (RFC
// 4825 5.7).
//
// SIMPLIFIED: RFC 4825 8. The server reads the document without
// authenticating the request. The Digest username is a private user
// identity, and Sh finds a user's credential by a public identity, which
// only the XUI of a document in a user's home directory gives. The
// document holds nothing about any user.
func serveCaps(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet, http.MethodHead:
	default:
		w.Header().Set("Allow", "GET")
		http.Error(w, "the capabilities document can only be read", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("ETag", capsETag)
	if status := precondition(r, capsETag, true); status != 0 {
		w.WriteHeader(status)
		return
	}
	w.Header().Set("Content-Type", capsMIMEType)
	w.Write(capsDocument)
}
