// Package skypicker provides a reusable client for the Skypicker Locations API.
package skypicker

// Airport contains the details returned by an airport lookup.
type Airport struct {
	Code        string
	AirportName string
	CityName    string
	Country     string
}
