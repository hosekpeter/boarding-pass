package bcbp

// Format M has fixed-width ASCII fields. Shorter values keep their padding;
// inserting or deleting bytes would move every following field.
// Ranges below use Go's convention: start inclusive, end exclusive.
// Reference: IATA Resolution 792, BCBP Implementation Guide, section 2.2.
const (
	maxPayloadSize = 4096

	// Header: format (1), leg count (1), passenger name (20), ticket indicator (1).
	formatIndex          = 0
	legCountIndex        = 1
	passengerNameStart   = 2
	passengerNameEnd     = 22
	ticketIndicatorIndex = 22
	headerSize           = 23

	// Each flight leg starts with this 37-byte block. Its conditional data
	// follows immediately; the final two bytes give that data's length in hex.
	bookingReferenceStart = 0
	bookingReferenceEnd   = 7
	originStart           = 7
	originEnd             = 10
	destinationStart      = 10
	destinationEnd        = 13
	airlineStart          = 13
	airlineEnd            = 16
	flightNumberStart     = 16
	flightNumberEnd       = 21
	julianDateStart       = 21
	julianDateEnd         = 24
	cabinClassIndex       = 24
	seatStart             = 25
	seatEnd               = 29
	// Bytes 29:34 hold the check-in sequence, which is not exposed by the API.
	passengerStatusStart   = 34
	passengerStatusEnd     = 35
	conditionalLengthStart = 35
	conditionalLengthEnd   = 37
	legSize                = 37

	// Optional security header: marker (1), type (1), hex data length (2).
	securityLengthStart = 2
	securityLengthEnd   = 4
	securityHeaderSize  = 4
)
