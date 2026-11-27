package ue_test

import (
	"context"
	"errors"
	"net"
	"os"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/amr"
	"github.com/yuu-biz/ims-practice/internal/rtp"
	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
	"github.com/yuu-biz/ims-practice/internal/ue"
)

// listenRTP is a bare socket on a free port of the loopback, where a
// test reads the RTP a UE sends as it went on the wire.
func listenRTP(t *testing.T) *net.UDPConn {
	t.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func portOf(conn *net.UDPConn) uint16 { return uint16(conn.LocalAddr().(*net.UDPAddr).Port) }

// drainFrames reads the RTP that has reached conn, until none comes for a
// while, and gives the AMR or AMR-WB frames of the packets of payload
// type codec, unpacked in its format.
func drainFrames(t *testing.T, conn *net.UDPConn, codec sdp.Codec) []amr.Frame {
	t.Helper()
	format, err := codec.AMRFormat()
	if err != nil {
		t.Fatal(err)
	}
	var frames []amr.Frame
	buf := make([]byte, 1500)
	for {
		conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		n, err := conn.Read(buf)
		if errors.Is(err, os.ErrDeadlineExceeded) {
			return frames
		}
		if err != nil {
			t.Fatal(err)
		}
		p, err := rtp.Unmarshal(buf[:n])
		if err != nil {
			t.Fatal(err)
		}
		if int(p.PayloadType) != codec.PayloadType {
			continue
		}
		payload, err := format.Unmarshal(p.Payload)
		if err != nil {
			t.Fatal(err)
		}
		frames = append(frames, payload.Frames...)
	}
}

// frameTypes lists the distinct frame types of frames, lowest first.
func frameTypes(frames []amr.Frame) []amr.FrameType {
	var types []amr.FrameType
	for _, f := range frames {
		if !slices.Contains(types, f.Type) {
			types = append(types, f.Type)
		}
	}
	slices.Sort(types)
	return types
}

// TestCalleeTakesOnlyAModeSetItsSpeechKeepsTo is RFC 4867 8.1, 8.2 and
// 8.3.1. The UE's recordings are AMR-WB 23.85 (mode 8) and AMR 12.2 (mode
// 7). An answerer returns an offered mode-set unmodified or removes the
// payload type, and frames of modes outside the mode-set must not be
// sent. So the UE takes no payload type whose mode-set leaves its mode
// out: it answers with the next format of the offer, or 488 when none is
// left. When it takes a mode-set, every frame it sends is of a mode in it.
func TestCalleeTakesOnlyAModeSetItsSpeechKeepsTo(t *testing.T) {
	for _, tc := range []struct {
		name    string
		formats string // of the m= line, then its a= lines
		status  int
		// The format the answer takes, as payload type and a=fmtp, and the
		// frame types the UE then sends.
		pt     int
		fmtp   string
		frames []amr.FrameType
	}{
		{"another codec", "96 98 101\r\n" +
			"a=rtpmap:96 AMR-WB/16000/1\r\na=fmtp:96 mode-set=0,1,2\r\n" +
			"a=rtpmap:98 AMR/8000/1\r\n" +
			"a=rtpmap:101 telephone-event/8000\r\n",
			200, 98, "mode-change-capability=2; max-red=0", []amr.FrameType{7}},
		{"no codec left", "96\r\n" +
			"a=rtpmap:96 AMR-WB/16000/1\r\na=fmtp:96 mode-set=0,1,2\r\n",
			488, 0, "", nil},
		{"a mode-set with the mode", "96 100\r\n" +
			"a=rtpmap:96 AMR-WB/16000/1\r\na=fmtp:96 mode-set=2,8\r\n" +
			"a=rtpmap:100 telephone-event/16000\r\n",
			200, 96, "mode-set=2,8; mode-change-capability=2; max-red=0", []amr.FrameType{8}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			caller := listenRTP(t)
			offer := "v=0\r\no=- 1 1 IN IP4 127.0.0.1\r\ns=-\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\n" +
				"m=audio " + strconv.Itoa(int(portOf(caller))) + " RTP/AVP " + tc.formats
			call, calleeMedia, answered := callVoLTECallee(t, offer)
			var answer *sdp.Session
			final := 0
			for resp := range call.Responses() {
				final = resp.StatusCode
				if len(resp.Body) > 0 && answer == nil {
					a, err := sdp.Parse(resp.Body)
					if err != nil {
						t.Fatal(err)
					}
					answer = &a
				}
			}
			if final != tc.status {
				t.Fatalf("final response %d, want %d", final, tc.status)
			}
			if tc.status != 200 {
				wait(t, "the callee", answered)
				return
			}
			codecs := answer.AudioMedia().Codecs()
			if codecs[0].PayloadType != tc.pt || codecs[0].Fmtp != tc.fmtp {
				t.Errorf("the answer takes %d with %q, want %d with %q", codecs[0].PayloadType, codecs[0].Fmtp, tc.pt, tc.fmtp)
			}
			select {
			case <-calleeMedia.Played():
			case <-time.After(5 * time.Second):
				t.Fatal("the callee did not send its speech")
			}
			if err := call.Bye(); err != nil {
				t.Fatal(err)
			}
			wait(t, "the callee", answered)
			sent := drainFrames(t, caller, codecs[0])
			if len(sent) == 0 || !slices.Equal(frameTypes(sent), tc.frames) {
				t.Errorf("the callee sent %d frames of types %v, want frames of %v", len(sent), frameTypes(sent), tc.frames)
			}
		})
	}
}

// callVoLTECallee sends offer to a UE that takes calls with the codecs of
// IR.92 and plays its AMR and AMR-WB tones once it answers. It returns the
// INVITE, the media of the UE, and the end of its Answer.
func callVoLTECallee(t *testing.T, offer string) (*sip.OutgoingCall, *ue.Media, <-chan error) {
	t.Helper()
	codecs, err := ue.CodecsNamed(ue.VoLTECodecNames)
	if err != nil {
		t.Fatal(err)
	}
	calleeTransport := listenTransport(t)
	calleeAddr := calleeTransport.LocalAddr()
	calls := ue.CallConfig{Codecs: codecs, NoPreconditions: true}
	calleeConfig := ue.Config{PrivateIdentity: impi2, PublicIdentity: impu2, Password: password2, HomeDomain: domain,
		PCSCF: calleeAddr, PCSCFURI: "sip:pcscf." + domain + ":5060", Calls: calls}
	calleeContact := ue.NewContact(calleeConfig, calleeAddr)
	incoming := make(chan *sip.IncomingCall, 1)
	calleeUA, err := sip.NewUA(calleeTransport, ue.UAConfig(sip.UAConfig{
		SentBy:  calleeAddr.String(),
		Contact: calleeContact.URI(),
		OnCall:  func(ic *sip.IncomingCall) { incoming <- ic },
	}, calls), quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(calleeUA.Close)
	callee := ue.New(calleeConfig, calleeUA.Stack(), calleeContact, quiet())
	media := listenMedia(t)

	callerUA := newTestUA(t, "caller", func(ic *sip.IncomingCall) { ic.Reject(486) })
	call, err := callerUA.Invite(sip.Invitation{
		RequestURI: calleeContact.URI(),
		From:       impu,
		Body:       sip.Body{Type: "application/sdp", Content: []byte(offer)},
	})
	if err != nil {
		t.Fatal(err)
	}
	answered := make(chan error, 1)
	go func() {
		select {
		case ic := <-incoming:
			answered <- callee.Answer(context.Background(), ic, media, ue.ToneSpeech(400*time.Millisecond),
				ue.AnswerOptions{PickUp: time.After(0)})
		case <-time.After(5 * time.Second):
			answered <- errors.New("no INVITE reached the callee")
		}
	}()
	return call, media, answered
}
