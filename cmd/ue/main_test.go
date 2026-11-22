package main

import (
	"bytes"
	"encoding/hex"
	"flag"
	"io"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/aka"
)

// TS 35.208 4.3.1, test set 1: K and OP give the OPc that the HSS of
// deploy/compose/hss.yaml has, so -op and -opc set up the same USIM.
func TestUSIMConfig(t *testing.T) {
	const k, op, opc = "465b5ce8b199b49faa5f0a2ee238a6bc", "cdc202d5123e20f62b6d676ac72cb318", "cd63cb71954a9f4e48a5994e37a02baf"
	fromOP, err := usimConfig(k, "", op, "000000000020")
	if err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(fromOP.Secret.OPc[:]); got != opc || fromOP.SQN != aka.SQN(0x20) {
		t.Errorf("from OP: OPc %s, SQN %d; want %s and 32", got, fromOP.SQN, opc)
	}
	fromOPc, err := usimConfig(k, opc, "", "000000000000")
	if err != nil || fromOPc.Secret != fromOP.Secret {
		t.Errorf("from OPc: %+v, %v", fromOPc, err)
	}
	for _, bad := range [][4]string{
		{k, "", "", "000000000000"},
		{k, opc, op, "000000000000"},
		{k[:30], opc, "", "000000000000"},
		{k, opc, "", "0020"},
	} {
		if _, err := usimConfig(bad[0], bad[1], bad[2], bad[3]); err == nil {
			t.Errorf("usimConfig%v: no error", bad)
		}
	}
}

// The JSON log leaves standard output to the stream of the call, so
// that a player reading it gets the audio and nothing else. Every other
// run keeps the log on standard output, where the scripts read it.
func TestLogLeavesStandardOutputToTheStreamOfTheCall(t *testing.T) {
	if w := logWriter(options{receivedOut: standardOutput}); w != os.Stderr {
		t.Error("the log of a call streaming to standard output does not go to standard error")
	}
	for _, o := range []options{{}, {receivedOut: "/captures/volte12-received.amr"}, {sentOut: "/captures/sent.amr"}} {
		if w := logWriter(o); w != os.Stdout {
			t.Errorf("the log of %+v does not go to standard output", o)
		}
	}
}

// One call has one standard output, so only -received-out takes it.
func TestOnlyReceivedOutTakesStandardOutput(t *testing.T) {
	if err := (options{receivedOut: standardOutput}).checkRecording(); err != nil {
		t.Errorf("-received-out %s: %v", standardOutput, err)
	}
	for _, o := range []options{{sentOut: standardOutput}, {wavOut: standardOutput}} {
		if err := o.checkRecording(); err == nil {
			t.Errorf("%+v: no error", o)
		}
	}
}

// flagNames are the names of the flags that command defines.
func flagNames(t *testing.T, command string) []string {
	t.Helper()
	flags, ok := newFlagSet(command, new(options), flag.ContinueOnError)
	if !ok {
		t.Fatalf("no command %q", command)
	}
	var names []string
	flags.VisitAll(func(f *flag.Flag) { names = append(names, f.Name) })
	return names
}

// "ue register -h" lists the flags of registration, and none of the
// flags of a call.
func TestRegisterHelpListsOnlyItsFlags(t *testing.T) {
	var o options
	flags, _ := newFlagSet("register", &o, flag.ContinueOnError)
	var help bytes.Buffer
	flags.SetOutput(&help)
	if err := flags.Parse([]string{"-h"}); err != flag.ErrHelp {
		t.Fatalf("Parse(-h) = %v; want flag.ErrHelp", err)
	}
	listed := map[string]bool{}
	for _, line := range strings.Split(help.String(), "\n") {
		if strings.HasPrefix(line, "  -") {
			listed[strings.Fields(line)[0]] = true
		}
	}
	for _, want := range []string{"-impi", "-impu", "-password", "-pcscf", "-k", "-opc", "-expires", "-deregister-after"} {
		if !listed[want] {
			t.Errorf("ue register -h does not list %s:\n%s", want, help.String())
		}
	}
	for _, not := range []string{"-ring", "-wav-in", "-control", "-tone", "-status-report", "-give-up-after", "-xcap-root"} {
		if listed[not] {
			t.Errorf("ue register -h lists %s", not)
		}
	}
}

// Each command rejects the flags that belong to the other commands,
// rather than ignoring them.
func TestCommandsRejectTheFlagsOfOthers(t *testing.T) {
	commands := []string{"register", "call", "answer", "serve", "sms", "xcap", "ctl"}
	all := map[string]bool{}
	for _, c := range commands {
		for _, name := range flagNames(t, c) {
			all[name] = true
		}
	}
	for _, c := range commands {
		own := flagNames(t, c)
		for name := range all {
			if slices.Contains(own, name) {
				continue
			}
			var o options
			flags, _ := newFlagSet(c, &o, flag.ContinueOnError)
			flags.SetOutput(io.Discard)
			err := flags.Parse([]string{"-" + name + "=1"})
			if err == nil || !strings.Contains(err.Error(), "flag provided but not defined") {
				t.Errorf("ue %s -%s: err = %v; want flag provided but not defined", c, name, err)
			}
		}
	}
}

// The flags that each command defines are the ones its code reads: run
// for register, call, answer and sms, serve, runXCAP, and runCtl.
func TestFlagsOfEachCommand(t *testing.T) {
	registration := []string{"domain", "impi", "impu", "password", "k", "opc", "op", "sqn", "port-s", "pcscf", "epc", "pcscf-uri", "listen", "hep", "expires", "features"}
	media := []string{"media-listen", "codecs", "no-preconditions"}
	audio := []string{"wav-in", "amr-in", "tone"}
	recording := []string{"wav-out", "sent-out", "received-out"}
	answering := []string{"ring", "reject"}
	for command, want := range map[string][]string{
		"register": slices.Concat(registration, []string{"deregister-after"}),
		"call":     slices.Concat(registration, media, audio, recording, []string{"give-up-after"}),
		"answer":   slices.Concat(registration, media, audio, recording, answering),
		"serve":    slices.Concat(registration, media, audio, answering, []string{"sc", "control", "xcap-root", "ut-password"}),
		"sms":      slices.Concat(registration, []string{"sc", "status-report", "report-wait"}),
		"xcap":     {"domain", "impi", "impu", "password", "xcap-root", "ut-password"},
		"ctl":      {"control"},
	} {
		got := flagNames(t, command)
		slices.Sort(got)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("ue %s defines %v; want %v", command, got, want)
		}
	}
}
