// Package h248 reads and writes H.248 (Megaco) messages in the text
// encoding of ITU-T H.248.1 Annex B, as far as the 3GPP profiles for
// Iq (TS 29.334), Ix (TS 29.238) and Mp (TS 29.333) use the protocol.
//
// The types carry the names of the grammar (Annex B.2) and of the
// ASN.1 syntax (Annex A.2), so that a learner can hold a capture, the
// specification and this code side by side. The profiles reference
// H.248.1 (05/2002), version 2, with its Corrigendum 1 (03/2004), and
// the grammar follows that text. Comments that cite RFC 3525 point to
// the text of version 1, which version 2 left unchanged in the places
// cited.
//
// Parse accepts the long and the short form of every token, in any
// case, as the profiles require of a receiver (TS 29.334 5.9). Marshal
// writes the long form, one item per line, because learners read these
// messages in a capture.
//
// Endpoint carries transactions over UDP with the procedures of
// H.248.1 Annex D.1: it repeats requests, carries out each request it
// receives at most once, and answers repetitions from the reply it
// keeps.
package h248
