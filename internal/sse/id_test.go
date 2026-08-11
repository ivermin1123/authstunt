package sse

import (
	"errors"
	"testing"
)

func TestEventIDRoundTrip(t *testing.T) {
	for _, id := range []EventID{
		{Generation: 1, Seq: 1},
		{Generation: 12, Seq: 0},
		{Generation: 9007199254740993, Seq: 4294967296},
	} {
		got, err := ParseEventID(id.String())
		if err != nil {
			t.Errorf("ParseEventID(%q): %v", id.String(), err)
			continue
		}
		if got != id {
			t.Errorf("ParseEventID(%q) = %v", id.String(), got)
		}
	}
}

func TestParseEventIDRejectsWhatWeNeverIssued(t *testing.T) {
	// Each of these would otherwise be read as a plausible id and compared
	// against real ones as though the client had been handed it.
	for _, s := range []string{
		"",
		"7",
		"-",
		"3-",
		"-3",
		"3-4-5",
		"+3-4",
		"3-+4",
		"3--4",
		" 3-4",
		"3-4 ",
		"0x3-4",
		"three-four",
		"3-4.5",
		"99999999999999999999-1",
	} {
		if got, err := ParseEventID(s); !errors.Is(err, ErrBadEventID) {
			t.Errorf("ParseEventID(%q) = %v, %v; want ErrBadEventID", s, got, err)
		}
	}
}

func TestZeroEventIDIsNotAnIssuedID(t *testing.T) {
	// The zero value has to mean "no id at all" for a fresh connection to be
	// distinguishable from one resuming at the start of a generation. Nothing
	// issues generation 0, because the store's counter starts at 1.
	if !(EventID{}).IsZero() {
		t.Error("the zero EventID does not report itself as zero")
	}
	if (EventID{Generation: 1, Seq: 0}).IsZero() {
		t.Error("generation 1 seq 0 is an issuable anchor and must not read as zero")
	}
}
