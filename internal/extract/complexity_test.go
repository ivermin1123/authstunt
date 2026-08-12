package extract

import (
	"strings"
	"testing"
	"time"
)

// TestExtractSurvivesAdversarialBodies guards the complexity of the engine.
//
// Every byte here is attacker-influenced: anyone who can send mail to a
// catch-all inbox writes this input, and the message size limit is 10MB, so
// each shape below is something a single message can legitimately contain.
// Five of these were superlinear when first written. The worst extrapolated to
// roughly an hour of CPU for one 10MB message, which is one message away from
// taking the extraction pool down with it.
//
// Two numbers, both chosen rather than inherited:
//
// The size is 2MB, not the full 10MB. What is being tested is the growth rate,
// not a wall-clock figure for the largest legal message, and 2MB already
// separates the two cases by a wide margin: the quadratic version of the worst
// shape here needed two minutes for 500KB, which puts 2MB at well over half an
// hour. Running the whole set at the real 10MB limit works and was measured -
// 6.8s plain, 71.4s under -race - but it costs that 71s of every race-enabled
// run to say what 14.2s already says. The 10MB figure is a measurement, not a
// gate; what is committed here runs at 2MB.
//
// The bound is 30 seconds per shape, against a worst shape measured at 4.5s
// under -race. That is roughly seven times the headroom against a slow CI
// runner while still catching a return to quadratic behavior by two orders of
// magnitude, so this can fail for a real reason and not for a busy machine.
func TestExtractSurvivesAdversarialBodies(t *testing.T) {
	const (
		size  = 2 << 20
		bound = 30 * time.Second
	)
	cases := []struct {
		name string
		in   Input
	}{
		{
			// trimURLTail used to recount brackets on every character it
			// dropped.
			name: "url followed by a long run of closing brackets",
			in:   Input{Text: "https://a.test/" + strings.Repeat(")", size)},
		},
		{
			// scoreCandidate used to walk every keyword match per candidate.
			name: "context word and candidate repeated together",
			in:   Input{Text: strings.Repeat("code 123456 ", size/12)},
		},
		{
			// Two separate quadratics lived here: the URL exclusion list was
			// scanned once per candidate, and the line around each URL was
			// found by scanning outwards for a newline, on a body that has
			// none.
			name: "many urls on one line each holding digits",
			in:   Input{Text: strings.Repeat("https://a.test/1234 ", size/20)},
		},
		{
			// The anchor text used to be built by string concatenation, one
			// append per text token.
			name: "one anchor holding many text tokens",
			in:   Input{HTML: `<a href="https://a.test/v">` + strings.Repeat("<b>y</b>", size/8)},
		},
		{
			// The scheme-less scanner walks the same body a second time, and
			// every match it keeps is trimmed and range-checked against the
			// URLs that do have a scheme.
			name: "many scheme-less urls on one line each holding digits",
			in:   Input{Text: strings.Repeat("www.a.test/1234 ", size/16)},
		},
		{
			name: "one enormous run of scheme-less url markers",
			in:   Input{Text: strings.Repeat("www.", size/4)},
		},
		{
			// Every href is reported now, not only an anchor's, so a body can
			// be one long list of them.
			name: "many non-anchor hrefs",
			in:   Input{HTML: strings.Repeat(`<area href="https://a.test/v">`, size/30)},
		},
		{
			name: "one enormous digit run",
			in:   Input{Text: strings.Repeat("1", size)},
		},
		{
			name: "scheme markers with no scheme",
			in:   Input{Text: strings.Repeat("://", size/3)},
		},
		{
			name: "every byte a combining mark",
			in:   Input{Text: strings.Repeat("ã", size/3)},
		},
		{
			name: "deeply repeated anchors",
			in:   Input{HTML: strings.Repeat(`<a href="https://a.test/v">v</a>`, size/32)},
		},
		{
			name: "unclosed tag holding the whole body",
			in:   Input{HTML: "<a href=\"https://a.test/v\"" + strings.Repeat(" x", size/2)},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Now()
			Extract(tc.in)
			if elapsed := time.Since(start); elapsed > bound {
				t.Errorf("took %v for a %d byte body, over the %v bound: complexity regressed",
					elapsed.Round(time.Millisecond), size, bound)
			}
		})
	}
}
