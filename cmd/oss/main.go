// Command oss stands in for the OSS: the network manager (NM) that
// manages the network over Itf-N, the interface between the element
// manager and the network manager (TS 32.101 5.1.1, TS 32.102 7.3.2).
// Here the OAM server is the element manager. Towards the OSS it is the
// MnS producer of fault supervision (TS 28.111), and the OSS is its MnS
// consumer: the OSS reads the AlarmList of the SubNetwork, acknowledges
// alarm records, and subscribes to the alarm notifications, which the
// OAM posts to it.
//
//	oss serve     takes the notifications the OAM posts, checks each,
//	              keeps those it takes, and serves them back
//	oss subscribe creates an NtfSubscriptionControl on the OAM whose
//	              notificationRecipientAddress is that of oss serve, or
//	              replaces it, and prints the subscription
//	oss unsubscribe
//	              deletes the NtfSubscriptionControl, which ends the
//	              subscription
//	oss alarms    reads the AlarmList and prints it
//	oss ack ALARM-ID...
//	              acknowledges the alarm records, or with -unacknowledge
//	              takes the acknowledgement back
//
// The last four send the ProvMnS requests of TS 28.532 12.1.1.1, a PUT,
// a DELETE, a GET and a JSON Merge Patch, to {MnSRoot}/ProvMnS/v19
// (TS 32.158 4.4.2), with the bodies built from the same types, and
// read the answers as strictly as serve reads the notifications.
//
// The stand-in does no network management of its own. What it adds over
// any HTTP server is the check of what the OAM sends: each notification
// must decode into the types of internal/mns, which carry the attribute
// names of the OpenAPI files of 3GPP, with no attribute those types do
// not have; the body must be the one its notificationType names; every
// attribute that the schema requires must be there; and the href and
// systemDN of the header must be a canonical URI and a DN. A
// notification that passes is answered with 204 (TS 32.158 5.5.4) and
// kept; one that fails is answered with 400 and an ErrorResponse that
// says why, and is kept apart with that reason, so that a script finds
// both.
//
// It checks what the OAM of this project sends, not what any MnS
// producer may send: it is no general MnS consumer. It refuses these
// bodies, which the schemas of TS28111_FaultNrm.yaml and
// TS28623_ComDefs.yaml allow:
//
//   - a header with sequenceNo or subscriptionId, which a producer
//     writes only with the reliable notification transport;
//   - a body with an optional attribute that the internal/mns types
//     leave out, such as backedUpStatus or correlatedNotifications, and
//     a notification with no internal/mns type, such as
//     notifyNewSecAlarm or notifyComments;
//   - a probableCause or a specificProblem given as an integer;
//   - an alarmType that internal/mns does not list, the types of the
//     security alarms;
//   - a notifyChangedAlarmGeneral without perceivedSeverity, or with
//     CLEARED, which the schema allows and TS 28.111 8.4.2 does not;
//   - null for any attribute, and an attribute given twice.
//
// The serve command keeps what it takes in memory only: it is a test
// stand-in, and a new start begins empty. The defaults of the flags are
// the addresses of compose.yaml.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/yuu-biz/ims-practice/internal/mns"
)

const usage = `usage: oss serve [-listen host:port]
       oss subscribe [flags]
       oss unsubscribe [flags]
       oss alarms [-filter EXPRESSION] [flags]
       oss ack [-user ID] [-system ID] [-unacknowledge] [flags] ALARM-ID...

"oss COMMAND -h" lists the flags of COMMAND.
`

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	// docker stop sends SIGTERM; a learner at a terminal, an interrupt.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	err := run(ctx, log, os.Args[1:], os.Stdout)
	stop()
	switch {
	case errors.Is(err, flag.ErrHelp):
	case errors.Is(err, errUsage):
		os.Exit(2)
	case err != nil:
		fmt.Fprintln(os.Stderr, "oss:", err)
		os.Exit(1)
	}
}

// errUsage is the answer to a command line that oss cannot run, once
// it has said why and printed the usage.
var errUsage = errors.New("usage")

// run runs the command of args, until it is done or ctx ends, and
// prints what it reads to out. -h, alone or after a command, prints the
// usage and returns flag.ErrHelp.
func run(ctx context.Context, log *slog.Logger, args []string, out io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return errUsage
	}
	command, args := args[0], args[1:]
	flags := flag.NewFlagSet("oss "+command, flag.ContinueOnError)
	switch command {
	case "-h", "-help", "--help", "help":
		fmt.Print(usage)
		return flag.ErrHelp
	case "serve":
		listen := flags.String("listen", "0.0.0.0:8080", "the address to take the notifications on, and to serve them back")
		if err := parse(flags, args, 0); err != nil {
			return err
		}
		return serve(ctx, log, *listen)
	case "subscribe":
		newConsumer := consumerFlags(flags)
		id := flags.String("id", "1", "the id of the NtfSubscriptionControl")
		recipient := flags.String("recipient", "http://oss:8080"+notificationPath, "the notificationRecipientAddress: where oss serve takes the notifications")
		types := flags.String("types", "", "the notificationTypes, separated by commas; none means every one")
		filter := flags.String("filter", "", "the notificationFilter, a Jex conditions expression such as perceivedSeverity=\"CRITICAL\"")
		if err := parse(flags, args, 0); err != nil {
			return err
		}
		c, err := newConsumer()
		if err != nil {
			return err
		}
		attributes := mns.NtfSubscriptionControlAttributes{NotificationRecipientAddress: *recipient, NotificationFilter: *filter}
		if *types != "" {
			for t := range strings.SplitSeq(*types, ",") {
				if _, ok := bodyRequired[mns.NotificationType(t)]; !ok {
					return fmt.Errorf("-types: %q is not a notification of fault supervision that the OAM sends", t)
				}
				attributes.NotificationTypes = append(attributes.NotificationTypes, mns.NotificationType(t))
			}
		}
		subscription, _, err := c.subscribe(ctx, *id, attributes)
		if err != nil {
			return err
		}
		return printJSON(out, subscription)
	case "unsubscribe":
		newConsumer := consumerFlags(flags)
		id := flags.String("id", "1", "the id of the NtfSubscriptionControl")
		if err := parse(flags, args, 0); err != nil {
			return err
		}
		c, err := newConsumer()
		if err != nil {
			return err
		}
		return c.unsubscribe(ctx, *id)
	case "alarms":
		newConsumer := consumerFlags(flags)
		filter := flags.String("filter", "", "a Jex conditions expression that selects the alarm records, such as /AlarmList[id=\"1\"]/attributes/alarmRecords/*[perceivedSeverity=\"MAJOR\"]")
		if err := parse(flags, args, 0); err != nil {
			return err
		}
		c, err := newConsumer()
		if err != nil {
			return err
		}
		list, err := c.readAlarmList(ctx, *filter)
		if err != nil {
			return err
		}
		return printJSON(out, list)
	case "ack":
		newConsumer := consumerFlags(flags)
		user := flags.String("user", "noc-operator", "the ackUserId: the operator who acknowledges")
		system := flags.String("system", "oss-1", "the ackSystemId: the OSS the operator acknowledges from")
		unacknowledge := flags.Bool("unacknowledge", false, "set the ackState to UNACKNOWLEDGED, not ACKNOWLEDGED")
		if err := parse(flags, args, oneOrMore); err != nil {
			return err
		}
		c, err := newConsumer()
		if err != nil {
			return err
		}
		ackState := mns.Acknowledged
		if *unacknowledge {
			ackState = mns.Unacknowledged
		}
		return c.acknowledge(ctx, flags.Args(), ackState, *user, *system)
	}
	fmt.Fprintf(os.Stderr, "oss: no command %q\n%s", command, usage)
	return errUsage
}

// oneOrMore is the arguments of parse for a command that takes one
// argument or more.
const oneOrMore = -1

// parse parses the flags of a command, which takes exactly arguments
// arguments after them, or oneOrMore. The flag package prints what is
// wrong with a flag, and the flags, itself.
func parse(flags *flag.FlagSet, args []string, arguments int) error {
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return errUsage
	}
	switch n := flags.NArg(); {
	case arguments == oneOrMore && n == 0:
		return fmt.Errorf("%s needs at least one argument", flags.Name())
	case arguments != oneOrMore && n != arguments:
		return fmt.Errorf("%s takes %d arguments, not %d: %q", flags.Name(), arguments, n, flags.Args())
	}
	return nil
}

// consumerFlags adds the flags that every request to the OAM takes, and
// returns what builds the consumer from them once they are parsed.
func consumerFlags(flags *flag.FlagSet) func() (consumer, error) {
	root := flags.String("mns-root", "http://oam:8080"+mns.DefaultRoot, "{MnSRoot}: the scheme, host, port and root path of the OAM's management services")
	subNetwork := flags.String("subnetwork", "ims", "the name of the SubNetwork that the OAM manages")
	return func() (consumer, error) {
		r, err := mns.ParseMnSRoot(*root)
		if err != nil {
			return consumer{}, fmt.Errorf("-mns-root: %w", err)
		}
		dn := mns.SubNetworkDN(*subNetwork)
		if err := dn.Check(); err != nil {
			return consumer{}, fmt.Errorf("-subnetwork: %w", err)
		}
		// The OAM asks every node for its alarms before it answers.
		return consumer{root: r, subNetwork: dn, client: &http.Client{Timeout: 30 * time.Second}}, nil
	}
}

func printJSON(out io.Writer, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "%s\n", b)
	return err
}

// serve is "oss serve": the notification recipient on listen, until
// ctx ends.
func serve(ctx context.Context, log *slog.Logger, listen string) error {
	listener, err := net.Listen("tcp", listen)
	if err != nil {
		return fmt.Errorf("-listen: %w", err)
	}
	server := &http.Server{
		Handler:           newRecipient(log).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		// A body that has not come within this time is not coming.
		ReadTimeout: 10 * time.Second,
		IdleTimeout: time.Minute,
	}
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	log.Info("taking notifications", "address", listener.Addr().String(), "path", notificationPath)
	select {
	case err := <-served:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return server.Shutdown(shutdown)
}
