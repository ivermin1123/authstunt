// Package extract pulls the OTP and the links out of one message.
//
// Everything here is a pure function of its input: no clock, no store, no
// network, no goroutines. That is what lets the corpus in testdata pin the
// behavior exactly, and it is why the engine is built before the SMTP path
// that will feed it.
//
// Two ideas carry most of the package. All input is normalized to NFC first,
// because real mail arrives in NFC and NFD both and the two are
// indistinguishable on screen while being different bytes. Matching then runs
// on a folded view - lower case, diacritics dropped - so one keyword list
// covers "mã xác thực" and the "ma xac thuc" people type without accents.
//
// Extracted links carry a kind and, separately, whether anything is allowed to
// open them. Only http and https are, so a javascript: href is reported for an
// operator to see and never handed to a fixture as a place to go.
package extract
