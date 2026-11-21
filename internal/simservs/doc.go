// Package simservs reads and writes the settings of the MMTel
// supplementary services: the simservs document of TS 24.623 that a UE
// changes over Ut with XCAP, and the MMTelServices document of TS 29.364
// that the HSS keeps as Sh Repository Data for the MMTel AS.
//
// The package has three parts.
//
//   - Typed values of the documents, named after the elements of the
//     schemas, with Parse and Marshal functions and Validate.
//   - Document, an XML tree that keeps a document as it was written,
//     with the get, put and delete of a node by an XCAP node selector
//     (RFC 4825 6.3). An XCAP server stores and changes documents
//     through it; the HTTP side lives elsewhere.
//   - Pure functions that evaluate the rules of communication diversion
//     and communication barring against plain call information.
//
// The scope is what GSMA IR.92 2.3 makes mandatory for the network.
package simservs
