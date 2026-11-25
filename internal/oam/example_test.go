package oam_test

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/yuu-biz/ims-practice/internal/maintenance"
	"github.com/yuu-biz/ims-practice/internal/oam"
)

// pcscfNode stands for a P-CSCF node with its maintenance listener.
func pcscfNode(name string) *httptest.Server {
	node, err := maintenance.New(maintenance.Config{
		Node:          name,
		LogLevel:      new(slog.LevelVar),
		Functions:     []maintenance.Function{{Role: "pcscf"}},
		Configuration: func() any { return map[string]string{"node": name} },
	})
	if err != nil {
		panic(err)
	}
	mux := http.NewServeMux()
	mux.Handle(maintenance.PathPrefix, node.Handler())
	return httptest.NewServer(mux)
}

// Example shows how the oam role mounts the OAM API on its listener,
// and how an operator drains every P-CSCF at once.
func Example() {
	p1, p2 := pcscfNode("pcscf-1"), pcscfNode("pcscf-2")
	defer p1.Close()
	defer p2.Close()
	// The name and the inventory come from the configuration of the oam
	// role.
	server, err := oam.New(oam.Config{
		Name: "oam",
		Nodes: []oam.Endpoint{
			{Name: "pcscf-1", Roles: []string{"pcscf"}, URL: p1.URL},
			{Name: "pcscf-2", Roles: []string{"pcscf"}, URL: p2.URL},
		},
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	mux := http.NewServeMux()
	mux.Handle(oam.PathPrefix, server.Handler())
	listener := httptest.NewServer(mux)
	defer listener.Close()

	req, _ := http.NewRequest(http.MethodPut, listener.URL+"/oam/v1/roles/pcscf/administrative-state",
		strings.NewReader(`{"administrative_state": "SHUTTINGDOWN", "operator": "test-operator"}`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer resp.Body.Close()
	var answer oam.FanOut[maintenance.FunctionState]
	if err := json.NewDecoder(resp.Body).Decode(&answer); err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(resp.StatusCode, "succeeded:", answer.Succeeded, "failed:", answer.Failed)
	for _, n := range answer.Nodes {
		fmt.Println(n.Node, n.Result.Role, n.Result.AdministrativeState)
	}
	// Output:
	// 200 succeeded: 2 failed: 0
	// pcscf-1 pcscf LOCKED
	// pcscf-2 pcscf LOCKED
}
