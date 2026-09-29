package bcbp

import (
	"fmt"
	"strings"
	"testing"
)

// Payload decoded from the assignment's PDF. Keep its fixed-width spaces.
const sample = "M1CYPRIAN/MICHAL       ZKN85B KSCPRGFR 7774 105Y029C0126 149>5181W 5104BFR 00000000000002A0000000000000 0                          NY"

func TestParseSample(t *testing.T) {
	pass, err := Parse(sample)
	if err != nil {
		t.Fatal(err)
	}
	if pass.PassengerName != "CYPRIAN/MICHAL" || len(pass.Legs) != 1 {
		t.Fatalf("unexpected pass: %+v", pass)
	}
	want := Leg{BookingReference: "ZKN85B", AirlineCode: "FR", FlightNumber: "7774", JulianDate: 105,
		CabinClass: "economy", Seat: "29C", PassengerStatus: "1", Origin: Airport{Code: "KSC"}, Destination: Airport{Code: "PRG"}}
	if pass.Legs[0] != want {
		t.Fatalf("got %+v, want %+v", pass.Legs[0], want)
	}
}

func TestParseMultipleLegsAndSecurity(t *testing.T) {
	// Conditional data contains what looks like a leg; only the length determines
	// where the next actual leg starts. Last block contains a trailing space.
	first := sample[23:58] + "06" + "M2ABC "
	second := fmt.Sprintf("%-7s%s%s%-3s%-5s%s%s%s%-5s%s%s", "NEXT12", "PRG", "LHR", "BA", "0012A", "366", "C", "001A", "00002", "1", "00")
	pass, err := Parse("M2" + sample[2:23] + first + second + "^103SIG")
	if err != nil {
		t.Fatal(err)
	}
	if len(pass.Legs) != 2 || pass.Legs[1].FlightNumber != "12A" || pass.Legs[1].Seat != "1A" || pass.Legs[1].CabinClass != "business" || pass.Legs[1].JulianDate != 366 {
		t.Fatalf("unexpected itinerary: %+v", pass)
	}
}

func TestParseInvalid(t *testing.T) {
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
			if _, err := Parse(value); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestAllTruncations(t *testing.T) {
	for n := 0; n < len(sample); n++ {
		if _, err := Parse(sample[:n]); err == nil {
			t.Fatalf("accepted truncation at %d", n)
		}
	}
}

func TestUnknownCabinAndUnassignedSeat(t *testing.T) {
	payload := sample[:47] + "Z    " + sample[52:]
	pass, err := Parse(payload)
	if err != nil {
		t.Fatal(err)
	}
	if pass.Legs[0].CabinClass != "unknown" || pass.Legs[0].Seat != "" {
		t.Fatalf("unexpected leg: %+v", pass.Legs[0])
	}
}

func FuzzParse(f *testing.F) {
	f.Add(sample)
	f.Add("")
	f.Add("M1" + strings.Repeat(" ", 58))
	f.Fuzz(func(t *testing.T, payload string) {
		pass, err := Parse(payload)
		if err == nil && (len(pass.Legs) < 1 || len(pass.Legs) > 4 || pass.PassengerName == "") {
			t.Fatalf("invalid successful parse: %+v", pass)
		}
	})
}

func TestJulianDateFormatAndBounds(t *testing.T) {
	for _, tc := range []struct {
		value string
		valid bool
	}{
		{"001", true}, {"365", true}, {"366", true},
		{"+12", false}, {"-12", false}, {" 12", false}, {"12 ", false},
		{"000", false}, {"367", false}, {"1A2", false},
	} {
		t.Run(tc.value, func(t *testing.T) {
			_, err := Parse(sample[:44] + tc.value + sample[47:])
			if (err == nil) != tc.valid {
				t.Fatalf("date %q: %v", tc.value, err)
			}
		})
	}
}
