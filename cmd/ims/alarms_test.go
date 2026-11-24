package main

import (
	"log/slog"
	"net"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/config"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/maintenance"
	"github.com/yuu-biz/ims-practice/internal/metrics"
)

// TestRoleRaisesAnAlarmWhileItCannotReachAnHSS: of the two instances of
// the HSS, the second is taken out of service the way the drain of an
// HSS does it, with a DPR and no listener. The S-CSCF raises the alarm
// of that instance once its attempt to connect again fails, and clears
// it once the connection is open again. The first instance never has
// an alarm. The values are spelled as TS 28.111 spells them.
func TestRoleRaisesAnAlarmWhileItCannotReachAnHSS(t *testing.T) {
	if err := diameter.LoadDictionary(); err != nil {
		t.Fatal(err)
	}
	timers := diameter.Timers{Watchdog: time.Second, Reconnect: 100 * time.Millisecond, Answer: time.Second}
	hosts := []string{"hss-1." + domain, "hss-2." + domain}
	var servers []*diameter.Server
	for _, host := range hosts {
		hss := diameter.NewNode(diameter.Identity{OriginHost: host, OriginRealm: domain}, []uint32{diameter.ApplicationIDCx}, timers, quiet())
		server, err := hss.Listen("127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(server.Stop)
		servers = append(servers, server)
	}
	node := diameter.NewNode(diameter.Identity{OriginHost: "scscf." + domain, OriginRealm: domain}, []uint32{diameter.ApplicationIDCx}, timers, quiet())
	var peers []*diameter.Peer
	for i, host := range hosts {
		peers = append(peers, node.Connect(diameter.PeerConfig{Address: servers[i].Addr(), OriginHost: host}))
	}
	t.Cleanup(func() { diameter.StopAll(peers) })

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Node: "scscf", Roles: []config.Role{config.RoleSCSCF}, HomeDomain: domain,
		Maintenance: &config.Maintenance{Listen: l.Addr().String()}}
	roles := []managedRole{{function: maintenance.Function{Role: string(config.RoleSCSCF)},
		manage: func(*maintenance.ManagedFunction) {}, diameterPeers: peers}}
	stop, err := startMaintenance(l, cfg, roles, metrics.NewRegistry(), new(slog.LevelVar), quiet(), func() {})
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	api := maintenance.NewClient("http://"+l.Addr().String(), http.DefaultClient)

	// alarmsWhen waits up to five seconds until done says yes to the
	// alarm list of the node, and returns that list.
	alarmsWhen := func(what string, done func(maintenance.AlarmList) bool) maintenance.AlarmList {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			list, err := api.Alarms(t.Context(), maintenance.AlarmFilter{})
			if err != nil {
				t.Fatal(err)
			}
			if done(list) {
				return list
			}
			if time.Now().After(deadline) {
				t.Fatalf("no %s within 5 s; the alarm list holds %+v", what, list.AlarmRecords)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	list := alarmsWhen("open connection to both instances", func(maintenance.AlarmList) bool {
		return peers[0].Available() && peers[1].Available()
	})
	if len(list.AlarmRecords) != 0 {
		t.Fatalf("alarms %+v with both instances of the HSS open, want none", list.AlarmRecords)
	}

	if err := servers[1].SetAccepting(string(config.RoleHSS), false); err != nil {
		t.Fatal(err)
	}
	servers[1].DisconnectPeers()
	list = alarmsWhen("alarm of the second instance", func(l maintenance.AlarmList) bool { return len(l.AlarmRecords) > 0 })
	if len(list.AlarmRecords) != 1 {
		t.Fatalf("alarms %+v, want the one of %s alone", list.AlarmRecords, hosts[1])
	}
	got := list.AlarmRecords[0]
	want := maintenance.AlarmRecord{
		AlarmID:           got.AlarmID,
		ObjectInstance:    "ManagedElement=scscf,ManagedFunction=scscf,Peer=" + hosts[1],
		Role:              "scscf",
		Peer:              maintenance.Peer(hosts[1]),
		AlarmRaisedTime:   got.AlarmRaisedTime,
		AlarmType:         "COMMUNICATIONS_ALARM",
		ProbableCause:     "COMMUNICATIONS_SUBSYSTEM_FAILURE",
		SpecificProblem:   "Diameter peer down",
		PerceivedSeverity: "MAJOR",
		AdditionalText:    diameter.ReasonNoTransport,
		AckState:          "UNACKNOWLEDGED",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("alarm\n%+v, want\n%+v", got, want)
	}

	if err := servers[1].SetAccepting(string(config.RoleHSS), true); err != nil {
		t.Fatal(err)
	}
	list = alarmsWhen("clearing of the alarm", func(l maintenance.AlarmList) bool {
		return len(l.AlarmRecords) == 1 && l.AlarmRecords[0].PerceivedSeverity == maintenance.Cleared
	})
	if cleared := list.AlarmRecords[0]; cleared.AlarmID != got.AlarmID || cleared.AlarmClearedTime == nil {
		t.Errorf("cleared alarm %+v, want %s with its time of clearing", cleared, got.AlarmID)
	}
	if !peers[1].Available() {
		t.Error("the alarm is cleared, but the connection to the second instance takes no requests")
	}
}
