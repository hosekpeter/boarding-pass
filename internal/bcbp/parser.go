// Package bcbp parses the mandatory fields of IATA Resolution 792 format M.
package bcbp

import (
	"fmt"
	"strconv"
	"strings"
)

type BoardingPass struct {
	PassengerName string `json:"passenger_name" example:"CYPRIAN/MICHAL"`
	Legs          []Leg  `json:"legs" validate:"min=1,max=4"`
}

type Leg struct {
	BookingReference string `json:"booking_reference" example:"ZKN85B"`
	AirlineCode      string `json:"airline_code" example:"FR"`
	FlightNumber     string `json:"flight_number" example:"7774"`
	// Day of year; BCBP mandatory fields do not carry the flight year.
	JulianDate      int     `json:"julian_date" minimum:"1" maximum:"366" example:"105"`
	CabinClass      string  `json:"cabin_class" enums:"first,business,premium_economy,economy,unknown" example:"economy"`
	Seat            string  `json:"seat" example:"29C"`
	PassengerStatus string  `json:"passenger_status" example:"1"`
	Origin          Airport `json:"origin"`
	Destination     Airport `json:"destination"`
}

type Airport struct {
	Code        string `json:"code" pattern:"^[A-Z]{3}$" example:"PRG"`
	AirportName string `json:"airport_name" example:"Václav Havel Airport Prague"`
	CityName    string `json:"city_name" example:"Prague"`
	Country     string `json:"country" example:"Czechia"`
}

// Parse preserves fixed-width whitespace and skips each leg's conditional fields
// using its hexadecimal length. It does not verify optional digital signatures.
func Parse(payload string) (BoardingPass, error) {
	// Accept scanner line endings without trimming fixed-width space padding.
	payload = strings.TrimRight(payload, "\r\n")
	name, count, err := parseHeader(payload)
	pass := BoardingPass{PassengerName: name}
	if err != nil {
		return pass, err
	}

	pass.Legs = make([]Leg, 0, count)
	rest := payload[headerSize:]
	for i := range count {
		leg, consumed, err := parseLeg(rest)
		if err != nil {
			return BoardingPass{}, fmt.Errorf("flight leg %d: %w", i+1, err)
		}
		pass.Legs = append(pass.Legs, leg)
		rest = rest[consumed:]
	}

	if err := validateTrailer(rest); err != nil {
		return BoardingPass{}, err
	}
	return pass, nil
}

func parseHeader(payload string) (string, int, error) {
	if len(payload) < headerSize || len(payload) > maxPayloadSize {
		return "", 0, fmt.Errorf("invalid BCBP length")
	}
	// ASCII validation makes byte offsets equivalent to character positions.
	for _, b := range []byte(payload) {
		if b < 32 || b > 126 {
			return "", 0, fmt.Errorf("BCBP must contain printable ASCII")
		}
	}
	if payload[formatIndex] != 'M' || payload[legCountIndex] < '1' || payload[legCountIndex] > '4' {
		return "", 0, fmt.Errorf("expected format M with 1 to 4 flight legs")
	}

	name := strings.TrimSpace(payload[passengerNameStart:passengerNameEnd])
	if name == "" {
		return "", 0, fmt.Errorf("missing passenger name")
	}
	if payload[ticketIndicatorIndex] != 'E' && payload[ticketIndicatorIndex] != ' ' {
		return name, 0, fmt.Errorf("invalid electronic ticket indicator")
	}
	return name, int(payload[legCountIndex] - '0'), nil
}

// parseLeg returns the decoded leg and the number of bytes consumed, including
// the conditional fields that follow its fixed-width block.
func parseLeg(data string) (Leg, int, error) {
	if len(data) < legSize {
		return Leg{}, 0, fmt.Errorf("truncated mandatory fields")
	}
	raw := data[:legSize]
	dayText := raw[julianDateStart:julianDateEnd]
	for _, digit := range dayText {
		if digit < '0' || digit > '9' {
			return Leg{}, 0, fmt.Errorf("invalid Julian date")
		}
	}
	day, err := strconv.Atoi(dayText)
	if err != nil || day < 1 || day > 366 {
		return Leg{}, 0, fmt.Errorf("invalid Julian date")
	}

	origin := raw[originStart:originEnd]
	destination := raw[destinationStart:destinationEnd]
	carrier := strings.TrimSpace(raw[airlineStart:airlineEnd])
	if !letters(origin) || !letters(destination) || len(carrier) < 2 || !alphaNumeric(carrier) {
		return Leg{}, 0, fmt.Errorf("invalid airport or airline code")
	}
	flight := strings.TrimSpace(raw[flightNumberStart:flightNumberEnd])
	if flight == "" || !alphaNumeric(flight) || flight[0] < '0' || flight[0] > '9' {
		return Leg{}, 0, fmt.Errorf("invalid flight number")
	}
	seat := strings.TrimSpace(raw[seatStart:seatEnd])
	if seat != "" && !alphaNumeric(seat) {
		return Leg{}, 0, fmt.Errorf("invalid seat")
	}

	conditionalSize, err := hexLength(raw[conditionalLengthStart:conditionalLengthEnd])
	if err != nil {
		return Leg{}, 0, fmt.Errorf("invalid variable field length")
	}
	consumed := legSize + conditionalSize
	if consumed > len(data) {
		return Leg{}, 0, fmt.Errorf("truncated variable fields")
	}
	return Leg{
		BookingReference: strings.TrimSpace(raw[bookingReferenceStart:bookingReferenceEnd]),
		Origin:           Airport{Code: origin},
		Destination:      Airport{Code: destination},
		AirlineCode:      carrier,
		FlightNumber:     unpad(flight),
		JulianDate:       day,
		CabinClass:       cabin(raw[cabinClassIndex]),
		Seat:             unpad(seat),
		PassengerStatus:  strings.TrimSpace(raw[passengerStatusStart:passengerStatusEnd]),
	}, consumed, nil
}

// A security block, when present, follows all flight legs, not each leg.
func validateTrailer(rest string) error {
	if strings.HasPrefix(rest, "^") {
		if len(rest) < securityHeaderSize {
			return fmt.Errorf("truncated security header")
		}
		size, err := hexLength(rest[securityLengthStart:securityLengthEnd])
		if err != nil || size > len(rest)-securityHeaderSize {
			return fmt.Errorf("invalid security length")
		}
		rest = rest[securityHeaderSize+size:]
	}
	if strings.TrimSpace(rest) != "" {
		return fmt.Errorf("unexpected trailing BCBP data")
	}
	return nil
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
