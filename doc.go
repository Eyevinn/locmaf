// Package locmaf implements LOCMAF (Low Overhead CMAF for MOQ), a
// compact CMAF packaging for MoQ Transport defined by the Internet-Draft
// draft-einarsson-moq-locmaf.
//
// A LOCMAF Object payload is a self-delimiting element sequence: zero or
// more genBox elements (pre-moof boxes such as styp, prft, and emsg,
// carried verbatim), exactly one full or delta moof header, and the raw
// mdat payload. The receiver reconstructs a functionally lossless CMAF
// chunk; a normative canonical reconstruction makes conformant receivers
// byte-identical, enabling golden-vector conformance testing.
//
// This module is the reference implementation. The v0.3 codec is being
// ported here from moqlivemock (see the Eyevinn/moqlivemock repository
// for the current v0.2 implementation). The vi64 subpackage implements
// the MOQT variable-length integer encoding and the zigzag signed
// variant that LOCMAF uses.
//
// Specification: https://datatracker.ietf.org/doc/draft-einarsson-moq-locmaf/
package locmaf
