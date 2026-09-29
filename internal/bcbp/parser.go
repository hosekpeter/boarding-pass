// Package bcbp parses the mandatory fields of IATA Resolution 792 format M.
package bcbp

import (
	"fmt"
	"strconv"
	"strings"
)

type BoardingPass struct {
	PassengerName string `json:"passenger_name"`
	Legs          []Leg  `json:"legs"`
}

type Leg struct {
	BookingReference string  `json:"booking_reference"`
	AirlineCode      string  `json:"airline_code"`
	FlightNumber     string  `json:"flight_number"`
	JulianDate       int     `json:"julian_date"`
	CabinClass       string  `json:"cabin_class"`
	Seat             string  `json:"seat"`
	PassengerStatus  string  `json:"passenger_status"`
	Origin           Airport `json:"origin"`
	Destination      Airport `json:"destination"`
}

type Airport struct {
	Code        string `json:"code"`
	AirportName string `json:"airport_name"`
	CityName    string `json:"city_name"`
	Country     string `json:"country"`
}

// Parse preserves fixed-width whitespace and skips each leg's conditional fields
// using its hexadecimal length. It does not verify optional digital signatures.
func Parse(payload string) (BoardingPass, error) {
	var pass BoardingPass
	if len(payload) < 23 || len(payload) > 4096 {
		return pass, fmt.Errorf("invalid BCBP length")
	}

	for _, b := range []byte(payload) {
		if b < 32 || b > 126 {
			return pass, fmt.Errorf("BCBP must contain printable ASCII")
		}
	}

	if payload[0] != 'M' || payload[1] < '1' || payload[1] > '4' {
		return pass, fmt.Errorf("expected format M with 1 to 4 flight legs")
	}

	pass.PassengerName = strings.TrimSpace(payload[2:22])
	if pass.PassengerName == "" {
		return pass, fmt.Errorf("missing passenger name")
	}

	if payload[22] != 'E' && payload[22] != ' ' {
		return pass, fmt.Errorf("invalid electronic ticket indicator")
	}

	count := int(payload[1] - '0')
	pass.Legs = make([]Leg, 0, count)
	pos := 23

	for i := range count {
		if len(payload)-pos < 37 {
			return BoardingPass{}, fmt.Errorf("truncated flight leg %d", i+1)
		}
		raw := payload[pos : pos+37]
		for _, digit := range raw[21:24] {
			if digit < '0' || digit > '9' {
				return BoardingPass{}, fmt.Errorf("invalid Julian date in leg %d", i+1)
			}
		}
		day, err := strconv.Atoi(raw[21:24])
		if err != nil || day < 1 || day > 366 {
			return BoardingPass{}, fmt.Errorf("invalid Julian date in leg %d", i+1)
		}

		carrier := strings.TrimSpace(raw[13:16])
		if !letters(raw[7:10]) || !letters(raw[10:13]) || len(carrier) < 2 || !alphaNumeric(carrier) {
			return BoardingPass{}, fmt.Errorf("invalid airport or airline code in leg %d", i+1)
		}

		flight := strings.TrimSpace(raw[16:21])
		if flight == "" || !alphaNumeric(flight) || flight[0] < '0' || flight[0] > '9' {
			return BoardingPass{}, fmt.Errorf("invalid flight number in leg %d", i+1)
		}

		size, err := hexLength(raw[35:37])
		if err != nil {
			return BoardingPass{}, fmt.Errorf("invalid variable field length in leg %d", i+1)
		}

		pos += 37
		if size > len(payload)-pos {
			return BoardingPass{}, fmt.Errorf("truncated variable fields in leg %d", i+1)
		}

		pos += size
		pass.Legs = append(pass.Legs, Leg{
			BookingReference: strings.TrimSpace(raw[:7]),
			Origin:           Airport{Code: raw[7:10]},
			Destination:      Airport{Code: raw[10:13]},
			AirlineCode:      carrier,
			FlightNumber:     unpad(flight),
			JulianDate:       day,
			CabinClass:       cabin(raw[24]),
			Seat:             unpad(strings.TrimSpace(raw[25:29])),
			PassengerStatus:  strings.TrimSpace(raw[34:35]),
		})
	}
	// A security block, when present, follows all flight legs, not each leg.
	rest := payload[pos:]
	if strings.HasPrefix(rest, "^") {
		if len(rest) < 4 {
			return BoardingPass{}, fmt.Errorf("truncated security header")
		}
		size, err := hexLength(rest[2:4])
		if err != nil || size > len(rest)-4 {
			return BoardingPass{}, fmt.Errorf("invalid security length")
		}
		rest = rest[4+size:]
	}
	if strings.TrimSpace(rest) != "" {
		return BoardingPass{}, fmt.Errorf("unexpected trailing BCBP data")
	}
	return pass, nil
}

func hexLength(s string) (int, error) {
	for _, c := range s {
		if !strings.ContainsRune("0123456789ABCDEF", c) {
			return 0, fmt.Errorf("invalid hex length")
		}
	}
	n, err := strconv.ParseUint(s, 16, 8)
	return int(n), err
}

func letters(s string) bool {
	for _, c := range s {
		if c < 'A' || c > 'Z' {
			return false
		}
	}
	return s != ""
}

func alphaNumeric(s string) bool {
	for _, c := range s {
		if !(c >= 'A' && c <= 'Z') && !(c >= '0' && c <= '9') {
			return false
		}
	}
	return s != ""
}

func unpad(s string) string {
	v := strings.TrimLeft(s, "0")
	if v == "" && s != "" {
		return "0"
	}
	return v
}

func cabin(code byte) string {
	switch code {
	case 'F', 'P':
		return "first"
	case 'C', 'J':
		return "business"
	case 'W':
		return "premium_economy"
	case 'Y', 'M':
		return "economy"
	default:
		return "unknown"
	}
}
