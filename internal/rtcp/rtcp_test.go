package rtcp

import (
	"bytes"
	"encoding/hex"
	"reflect"
	"strings"
	"testing"
	"time"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.ReplaceAll(s, " ", ""))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestCompoundLayout checks the octets of a compound SR and SDES
// against the figures of RFC 3550 6.4.1 and 6.5.
func TestCompoundLayout(t *testing.T) {
	sr := SenderReport{
		SSRC: 0x11223344, NTPTime: 0xe8c0a5b0_80000000, RTPTime: 160, PacketCount: 50, OctetCount: 3050,
		Reports: []ReportBlock{{SSRC: 0x55667788, FractionLost: 26, CumulativeLost: -1, HighestSequence: 0x0001_0005,
			Jitter: 12, LastSR: 0xa5b08000, DelaySinceLastSR: 0x00018000}},
	}
	sdes := SourceDescription{Chunks: []Chunk{{SSRC: 0x11223344, CNAME: "ue@192.0.2.1"}}}
	got, err := Marshal(sr, sdes)
	if err != nil {
		t.Fatal(err)
	}
	want := mustHex(t, ""+
		// SR: V=2, RC=1, PT=200, length 12 words after the first.
		"81 c8 000c 11223344 e8c0a5b0 80000000 000000a0 00000032 00000bea"+
		// The report block: fraction 26, cumulative lost -1 in 24 bits.
		"55667788 1a ffffff 00010005 0000000c a5b08000 00018000"+
		// SDES: SC=1, PT=202; CNAME (1) of 12 octets, then the null
		// octet and padding to the 32-bit boundary.
		"81 ca 0005 11223344 01 0c "+hex.EncodeToString([]byte("ue@192.0.2.1"))+" 00 00")
	if !bytes.Equal(got, want) {
		t.Errorf("compound packet:\ngot  %x\nwant %x", got, want)
	}

	back, err := Unmarshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back, []Packet{sr, sdes}) {
		t.Errorf("Unmarshal = %+v", back)
	}
}

func TestReceiverReportAndBye(t *testing.T) {
	rr := ReceiverReport{SSRC: 7}
	bye := Goodbye{SSRCs: []uint32{7}, Reason: "call ended"}
	b, err := Marshal(rr, SourceDescription{Chunks: []Chunk{{SSRC: 7, CNAME: "ue@192.0.2.2"}}}, bye)
	if err != nil {
		t.Fatal(err)
	}
	back, err := Unmarshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(back) != 3 || !reflect.DeepEqual(back[0], ReceiverReport{SSRC: 7}) || !reflect.DeepEqual(back[2], bye) {
		t.Errorf("Unmarshal = %+v", back)
	}
	if len(b)%4 != 0 {
		t.Errorf("compound packet of %d octets is not 32-bit aligned", len(b))
	}
}

func TestUnmarshalChecks(t *testing.T) {
	good, _ := Marshal(ReceiverReport{SSRC: 1}, SourceDescription{Chunks: []Chunk{{SSRC: 1, CNAME: "a"}}})
	for name, b := range map[string][]byte{
		"version 1":        append([]byte{0x40}, good[1:]...),
		"starts with SDES": good[8:],
		"too long":         good[:len(good)-4],
		"padding not last": append([]byte{0xa0}, good[1:]...),
		"empty":            {},
	} {
		if _, err := Unmarshal(b); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// An APP packet (RFC 3550 6.7) is read as Unknown, not refused (IR.92 3.2.4).
	app := mustHex(t, "81 cc 0002 00000001 6e616d65")
	back, err := Unmarshal(append(append([]byte{}, good...), app...))
	if err != nil {
		t.Fatal(err)
	}
	if u, ok := back[2].(Unknown); !ok || u.PacketType != TypeAPP || u.Count != 1 {
		t.Errorf("APP read as %+v", back[2])
	}
	if _, err := Marshal(SourceDescription{}); err == nil {
		t.Error("Marshal started a compound packet with SDES")
	}
}

func TestNTPTime(t *testing.T) {
	// RFC 868: the Unix epoch is 2208988800 s after the NTP one.
	if got := NTPTime(time.Unix(0, 0)); got != 2208988800<<32 {
		t.Errorf("NTPTime(1970) = %#x", got)
	}
	if got := NTPTime(time.Unix(0, 500_000_000)); got&0xffffffff != 0x80000000 {
		t.Errorf("half a second = %#x of a second, want 0x80000000", got&0xffffffff)
	}
	if got := Middle32(0x1122334455667788); got != 0x33445566 {
		t.Errorf("Middle32 = %#x", got)
	}
}

// TestReceiverStatistics follows RFC 3550 A.3 and A.8 by hand: ten
// packets are expected and three lost, and packets 20 ms apart that
// arrive 20 ms apart have no jitter.
func TestReceiverStatistics(t *testing.T) {
	r := Receiver{SSRC: 9, ClockRate: 8000}
	start := time.Unix(1000, 0)
	if _, ok := r.Block(start); ok {
		t.Error("a report block before any packet")
	}
	for _, seq := range []uint16{100, 101, 103, 104, 105, 107, 109} {
		n := int(seq - 100)
		r.Packet(seq, uint32(160*n), start.Add(time.Duration(n)*20*time.Millisecond))
	}
	b, _ := r.Block(start.Add(time.Second))
	if b.HighestSequence != 109 || b.CumulativeLost != 3 || b.FractionLost != 3*256/10 || b.Jitter != 0 {
		t.Errorf("block %+v; want highest 109, lost 3, fraction %d, jitter 0", b, 3*256/10)
	}
	// The next interval starts from here: nothing lost since.
	r.Packet(110, 160*10, start.Add(200*time.Millisecond))
	if b, _ := r.Block(start.Add(2 * time.Second)); b.FractionLost != 0 || b.CumulativeLost != 3 {
		t.Errorf("second block %+v", b)
	}
}

func TestReceiverWrapAndJitter(t *testing.T) {
	r := Receiver{ClockRate: 16000}
	start := time.Unix(1000, 0)
	for i := range 6 {
		// Every other packet arrives 5 ms late: D is 80 units each time.
		late := time.Duration(i%2) * 5 * time.Millisecond
		r.Packet(uint16(65533+i), uint32(4294967000+320*i), start.Add(time.Duration(i)*20*time.Millisecond+late))
	}
	b, _ := r.Block(start.Add(time.Second))
	if b.HighestSequence != 1<<16+2 || b.CumulativeLost != 0 {
		t.Errorf("after the wrap: %+v", b)
	}
	// J grows by (80 - J)/16 five times.
	j := 0.0
	for range 5 {
		j += (80 - j) / 16
	}
	if b.Jitter < uint32(j)-1 || b.Jitter > uint32(j)+1 {
		t.Errorf("jitter %d, want about %.1f", b.Jitter, j)
	}
}

// feed sends packets 20 ms apart with timestamps that follow the
// sequence numbers, so that only the sequence numbers matter.
func feed(r *Receiver, seqs ...uint16) {
	start := time.Unix(1000, 0)
	for i, seq := range seqs {
		r.Packet(seq, uint32(160*i), start.Add(time.Duration(i)*20*time.Millisecond))
	}
}

// TestReceiverBackwardsJump covers RFC 3550 A.1: a jump of more than
// MAX_DROPOUT is not counted as received or lost at once. Only a second
// packet in sequence (bad_seq) shows that the source restarted. The
// cumulative loss must not overflow when the highest number moves back.
func TestReceiverBackwardsJump(t *testing.T) {
	r := Receiver{SSRC: 9, ClockRate: 8000}
	feed(&r, 50000, 40000)
	now := time.Unix(1000, 0).Add(time.Second)
	b, _ := r.Block(now)
	if b.HighestSequence != 50000 || b.CumulativeLost != 0 || b.FractionLost != 0 {
		t.Errorf("after one stray packet: %+v; want highest 50000, nothing lost", b)
	}
	// The next number in sequence confirms the restart.
	r.Packet(40001, 160*2, now)
	r.Packet(40002, 160*3, now.Add(20*time.Millisecond))
	b, _ = r.Block(now.Add(time.Second))
	if b.HighestSequence != 40002 || b.CumulativeLost != 0 || b.FractionLost != 0 {
		t.Errorf("after the restart: %+v; want highest 40002, nothing lost", b)
	}
}

// TestReceiverForwardJump: a jump forward by more than MAX_DROPOUT is
// handled the same way, and two jumps that are not in sequence never
// restart the statistics.
func TestReceiverForwardJump(t *testing.T) {
	r := Receiver{ClockRate: 8000}
	feed(&r, 100, 101, 5000, 9000, 20000, 102)
	b, _ := r.Block(time.Unix(1001, 0))
	if b.HighestSequence != 102 || b.CumulativeLost != 0 {
		t.Errorf("stray packets: %+v; want highest 102, nothing lost", b)
	}
	feed(&r, 5000, 5001)
	b, _ = r.Block(time.Unix(1002, 0))
	if b.HighestSequence != 5001 || b.CumulativeLost != 0 {
		t.Errorf("after the restart: %+v; want highest 5001, nothing lost", b)
	}
}

// TestReceiverReorderAndDuplicate: packets that arrive a little late
// or twice are inside MAX_MISORDER, so they count as received without
// moving the highest number, and a duplicate makes the loss negative.
func TestReceiverReorderAndDuplicate(t *testing.T) {
	r := Receiver{ClockRate: 8000}
	feed(&r, 100, 102, 101, 103)
	if b, _ := r.Block(time.Unix(1001, 0)); b.HighestSequence != 103 || b.CumulativeLost != 0 {
		t.Errorf("reordered: %+v; want highest 103, nothing lost", b)
	}
	feed(&r, 103, 90) // a duplicate, and a packet from before the first
	b, _ := r.Block(time.Unix(1002, 0))
	if b.HighestSequence != 103 || b.CumulativeLost != -2 || b.FractionLost != 0 {
		t.Errorf("duplicates: %+v; want highest 103, lost -2", b)
	}
}

func TestLastSR(t *testing.T) {
	r := Receiver{ClockRate: 8000}
	at := time.Unix(2000, 0)
	r.Packet(1, 0, at)
	r.SenderReport(SenderReport{NTPTime: 0x0000_1234_5678_0000}, at)
	b, _ := r.Block(at.Add(1500 * time.Millisecond))
	if b.LastSR != 0x12345678 || b.DelaySinceLastSR != 98304 {
		t.Errorf("LSR %#x, DLSR %d; want 0x12345678 and 1.5 s as 98304", b.LastSR, b.DelaySinceLastSR)
	}
}

func TestInterval(t *testing.T) {
	call := IntervalParams{Members: 2, Senders: 2, WeSent: true, RS: 0, RR: 4000, AvgSize: 120}
	if td := call.Deterministic(); td != MinInterval {
		t.Errorf("Td = %v; with 4000 bit/s the 5 s minimum applies", td)
	}
	call.Initial = true
	if td := call.Deterministic(); td != 2500*time.Millisecond {
		t.Errorf("initial Td = %v, want 2.5 s", td)
	}
	call.Initial = false
	lo, hi := call.Interval(0), call.Interval(0.999999)
	if lo < 2050*time.Millisecond || lo > 2055*time.Millisecond || hi < 6150*time.Millisecond || hi > 6160*time.Millisecond {
		t.Errorf("interval spans %v to %v, want 2.05 s to 6.16 s", lo, hi)
	}

	// Below the minimum bandwidth the share decides: 2 * 120 octets at 12.5 octets/s.
	slow := IntervalParams{Members: 2, Senders: 2, WeSent: true, RR: 100, AvgSize: 120}
	if td := slow.Deterministic(); td != 19200*time.Millisecond {
		t.Errorf("slow Td = %v, want 19.2 s", td)
	}
	// One sender in eight shares RS alone; the receivers share RR (RFC 3550 6.3.1 step 1).
	group := IntervalParams{Members: 8, Senders: 1, WeSent: true, RS: 400, RR: 1200, AvgSize: 1000}
	if td := group.Deterministic(); td != 20*time.Second {
		t.Errorf("sender Td = %v, want 20 s", td)
	}
	group.WeSent = false
	if td := group.Deterministic(); td < 46600*time.Millisecond || td > 46700*time.Millisecond {
		t.Errorf("receiver Td = %v, want 46.7 s", td)
	}
	if !(IntervalParams{}).Disabled() || call.Disabled() {
		t.Error("Disabled is wrong")
	}
	if got := UpdateAvgSize(100, 260); got != 110 {
		t.Errorf("UpdateAvgSize = %v, want 110", got)
	}
}
