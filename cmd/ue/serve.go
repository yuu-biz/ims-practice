package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yuu-biz/ims-practice/internal/ue"
)

// serve is "ue serve": a Phone with its HTTP API on -control. -ring and
// -reject give the answer policy until the controller sets another.
func serve(log *slog.Logger, o options) error {
	policy := ue.AnswerPolicy{Mode: ue.AnswerAuto, Ring: o.ringFor}
	if o.reject != 0 {
		if o.reject < 400 || o.reject > 699 {
			return fmt.Errorf("-reject %d is not a status from 400 to 699", o.reject)
		}
		policy = ue.AnswerPolicy{Mode: ue.AnswerReject, Reject: o.reject}
	}
	speech, err := o.speech()
	if err != nil {
		return err
	}
	line := ue.NewLine(true)
	opened, err := openUE(log, o, line)
	if err != nil {
		return err
	}
	defer opened.close()
	phone := ue.NewPhone(opened.ue, opened.ua, line, ue.PhoneConfig{MediaListen: o.mediaListen, Speech: speech, Policy: policy}, log)

	listener, err := net.Listen("tcp", o.control)
	if err != nil {
		return fmt.Errorf("-control: %w", err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer cancel()
	ctx, lost := whileAttached(ctx, opened.detached)
	// The supplementary services are under /xcap, next to the phone's
	// own API.
	mux := http.NewServeMux()
	mux.Handle("/", phone.Handler())
	// The XUI is the default public identity of the registration, which
	// the phone shows once registered (GSMA IR.92 2.3.2); a VoLTE UE
	// registers a temporary public identity that is barred.
	services := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		xui := phone.Status().IMPU
		if xui == "" {
			// The same {"error": ...} as the phone's own API.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "no public identity for the XUI before the phone registers"})
			return
		}
		ue.NewSupplementaryServices(xcapClient(o, xui)).Handler().ServeHTTP(w, r)
	})
	mux.Handle("/xcap", services)
	mux.Handle("/xcap/", services)
	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		// The requests end with ctx, the stream of /events among them.
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	go func() {
		if err := server.Serve(listener); !errors.Is(err, http.ErrServerClosed) {
			log.Error("control API stopped", "error", err.Error())
			cancel()
		}
	}()
	log.Info("control API listening", "address", listener.Addr().String())

	serveErr := phone.Serve(ctx)
	sctx, scancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer scancel()
	if lost() {
		// SIMPLIFIED: IR.92 2.4.2.1. The UE stops instead of setting up its
		// PDN connection again, as the other commands do (run).
		serveErr = errors.Join(serveErr, errDetached)
	}
	return errors.Join(serveErr, server.Shutdown(sctx))
}
