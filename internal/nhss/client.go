package nhss

import (
	"net/http"
	"strings"

	"github.com/yuu-biz/ims-practice/internal/sbi"
)

// Client sends the requests of an NF service consumer (the I-CSCF, the
// S-CSCF or an IMS AS) to the HSS. It keeps no state, so any instance
// of the consumer can use any instance of the HSS behind the apiRoot.
type Client struct {
	apiRoot string
	nfType  NFType
	http    *http.Client
}

// NewClient returns a client for the HSS whose apiRoot is apiRoot, such
// as "http://hss.ims.example" (TS 29.562 6.1.1 refers to TS 29.501
// 4.4.1 for its definition). nfType names the sender in User-Agent.
func NewClient(apiRoot string, nfType NFType) *Client {
	return &Client{apiRoot: strings.TrimSuffix(apiRoot, "/"), nfType: nfType, http: sbi.NewHTTPClient()}
}
