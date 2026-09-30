package bcbp

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Payload decoded from the assignment's PDF. Keep its fixed-width spaces.
const sample = "M1CYPRIAN/MICHAL       ZKN85B KSCPRGFR 7774 105Y029C0126 149>5181W 5104BFR 00000000000002A0000000000000 0                          NY"

func TestParseSample(t *testing.T) {
	t.Parallel()

	pass, err := Parse(sample)
	require.NoError(t, err)
	assert.Equal(t, "CYPRIAN/MICHAL", pass.PassengerName)
	require.Len(t, pass.Legs, 1)
	want := Leg{BookingReference: "ZKN85B", AirlineCode: "FR", FlightNumber: "7774", JulianDate: 105,
		CabinClass: "economy", Seat: "29C", PassengerStatus: "1", Origin: Airport{Code: "KSC"}, Destination: Airport{Code: "PRG"}}
	assert.Equal(t, want, pass.Legs[0])
}

func TestScannerLineEndings(t *testing.T) {
	t.Parallel()

	want, err := Parse(sample)
	require.NoError(t, err)
	for _, ending := range []string{"\n", "\r\n"} {
		got, err := Parse(sample + ending)
		require.NoError(t, err)
		assert.Equal(t, want, got)
	}
}

func TestSeatCharacters(t *testing.T) {
	t.Parallel()

	for _, seat := range []string{"!!!!", "02 C", "01a!"} {
		_, err := Parse(sample[:48] + seat + sample[52:])
		require.Error(t, err, seat)
	}
	pass, err := Parse(sample[:48] + "    " + sample[52:])
	require.NoError(t, err)
	assert.Empty(t, pass.Legs[0].Seat)
}

func TestFieldPaddingKeepsFollowingFieldsInPlace(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, passenger, airline, flight, seat string
	}{
		{"short values", "NOVAK/JAN", "FR", "7774", "29C"},
		{"extra letter", "NOVAK/JANA", "RYR", "12A", "1A"},
		{"full width values", "SMITH-JOHNSON/ALICEA", "RYR", "1234A", "123A"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Encode the documented field widths independently of parser offsets.
			payload := fmt.Sprintf("M1%-20sE%-7s%s%s%-3s%-5s%03d%s%4s%-5s%s%s",
				tc.passenger, "REF123", "KSC", "PRG", tc.airline, tc.flight, 105, "Y", tc.seat, "00001", "1", "00")
			pass, err := Parse(payload)
			require.NoError(t, err)
			assert.Equal(t, BoardingPass{
				PassengerName: tc.passenger,
				Legs: []Leg{{
					BookingReference: "REF123", Origin: Airport{Code: "KSC"}, Destination: Airport{Code: "PRG"},
					AirlineCode: tc.airline, FlightNumber: tc.flight, JulianDate: 105,
					CabinClass: "economy", Seat: tc.seat, PassengerStatus: "1",
				}},
			}, pass)
		})
	}
}

func TestParseMultipleLegsAndSecurity(t *testing.T) {
	t.Parallel()

	// Conditional data contains what looks like a leg; only the length determines
	// where the next actual leg starts. Last block contains a trailing space.
	first := sample[23:58] + "06" + "M2ABC "
	second := fmt.Sprintf("%-7s%s%s%-3s%-5s%s%s%s%-5s%s%s", "NEXT12", "PRG", "LHR", "BA", "0012A", "366", "C", "001A", "00002", "1", "00")
	pass, err := Parse("M2" + sample[2:23] + first + second + "^103SIG")
	require.NoError(t, err)
	require.Len(t, pass.Legs, 2)
	assert.Equal(t, "12A", pass.Legs[1].FlightNumber)
	assert.Equal(t, "1A", pass.Legs[1].Seat)
	assert.Equal(t, "business", pass.Legs[1].CabinClass)
	assert.Equal(t, 366, pass.Legs[1].JulianDate)
}

func TestParseInvalid(t *testing.T) {
	t.Parallel()

	replace := func(start, end int, value string) string { return sample[:start] + value + sample[end:] }
	cases := map[string]string{
		"empty": "", "short": "M1", "format": replace(0, 1, "X"), "zero legs": replace(1, 2, "0"), "too many legs": replace(1, 2, "5"),
		"missing name": replace(2, 22, strings.Repeat(" ", 20)), "non ASCII": replace(2, 3, "ě"), "ticket": replace(22, 23, "X"),
		"missing leg": replace(1, 2, "2"), "airport": replace(30, 33, "12X"), "airline": replace(36, 39, "   "),
		"day zero": replace(44, 47, "000"), "day too big": replace(44, 47, "367"), "invalid day": replace(44, 47, "XYZ"),
		"flight": replace(39, 44, "ABCDE"), "invalid hex": replace(58, 60, "GG"), "short variable": sample[:len(sample)-1],
		"trailing garbage": sample + "bad", "security header": sample + "^", "security body": sample + "^1FFshort",
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := Parse(value)
			assert.Error(t, err)
		})
	}
}

func TestAllTruncations(t *testing.T) {
	t.Parallel()

	for n := 0; n < len(sample); n++ {
		_, err := Parse(sample[:n])
		assert.Error(t, err, "accepted truncation at %d", n)
	}
}

func TestUnknownCabinAndUnassignedSeat(t *testing.T) {
	t.Parallel()

	payload := sample[:47] + "Z    " + sample[52:]
	pass, err := Parse(payload)
	require.NoError(t, err)
	require.Len(t, pass.Legs, 1)
	assert.Equal(t, "unknown", pass.Legs[0].CabinClass)
	assert.Empty(t, pass.Legs[0].Seat)
}

func FuzzParse(f *testing.F) {
	f.Add(sample)
	f.Add("")
	f.Add("M1" + strings.Repeat(" ", 58))
	f.Fuzz(func(t *testing.T, payload string) {
		pass, err := Parse(payload)
		if err == nil {
			assert.GreaterOrEqual(t, len(pass.Legs), 1)
			assert.LessOrEqual(t, len(pass.Legs), 4)
			assert.NotEmpty(t, pass.PassengerName)
		}
	})
}

func TestJulianDateFormatAndBounds(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		value string
		valid bool
	}{
		{"001", true}, {"365", true}, {"366", true},
		{"+12", false}, {"-12", false}, {" 12", false}, {"12 ", false},
		{"000", false}, {"367", false}, {"1A2", false},
	} {
		t.Run(tc.value, func(t *testing.T) {
			t.Parallel()

			_, err := Parse(sample[:44] + tc.value + sample[47:])
			if tc.valid {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}
