// Package locmaf implements LOCMAF (Low Overhead CMAF for MOQ), a
// compact CMAF packaging for MoQ Transport defined by the Internet-Draft
// draft-einarsson-moq-locmaf.
//
// A LOCMAF Object payload is a self-delimiting element sequence: zero or
// more genBox elements (pre-moof boxes such as styp, prft, and emsg,
// carried verbatim), exactly one full or delta moof header, and the raw
// mdat payload — or, alternatively, a single rawBoxes element carrying
// complete ISO BMFF boxes verbatim. The receiver reconstructs a
// functionally lossless CMAF chunk; a normative canonical reconstruction
// makes conformant receivers byte-identical, enabling golden-vector
// conformance testing.
//
// This module is the reference implementation. EncodeCanonical produces
// canonical LOCMAF Objects from CMAF moof boxes (choosing full or delta
// headers per the canonical rules), EncodeRaw wraps complete boxes as a
// rawBoxes Object, Decode applies deltas and deletions against the
// in-group State and yields the chunk's EffectiveValues (or the raw
// boxes verbatim), and ReconstructCanonical builds the byte-exact
// canonical CMAF chunk from the effective values. AppendFramed and
// NextFramed implement the length-prefixed self-framed carriage used
// outside MOQT (the .locmaf file format). The vi64 subpackage implements
// the MOQT variable-length integer encoding and the zigzag signed
// variant that LOCMAF uses.
//
// Specification: https://datatracker.ietf.org/doc/draft-einarsson-moq-locmaf/
package locmaf
