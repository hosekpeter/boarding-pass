package config

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"reflect"
	"strconv"

	"github.com/caarlos0/env/v11"
	"github.com/go-playground/validator/v10"
)

const DefaultLocationBaseURL = "https://api.skypicker.com"
const DefaultHTTPAddr = "127.0.0.1:8080"

type Config struct {
	LocationBaseURL string `env:"LOCATIONS_BASE_URL" envDefault:"https://api.skypicker.com" validate:"required,http_url"`
	HTTPAddr        string `env:"HTTP_ADDR" envDefault:"127.0.0.1:8080" validate:"required"`
	Database        DBConfig
}

// Load reads and validates configuration without creating resources.
func Load() (Config, error) {
	var cfg Config
	if err := env.Parse(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse environment: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate also supports callers that construct Config directly, such as the
// application scope and integration tests. Errors identify variables, not values.
func (c Config) Validate() error {
	check := validator.New()
	check.RegisterTagNameFunc(func(field reflect.StructField) string { return field.Tag.Get("env") })
	var problems []error
	invalid := make(map[string]bool)
	if err := check.Struct(c); err != nil {
		var fields validator.ValidationErrors
		if !errors.As(err, &fields) {
			return err
		}
		for _, field := range fields {
			invalid[field.Field()] = true
			problems = append(problems, fmt.Errorf("%s must satisfy %s", field.Field(), field.Tag()))
		}
	}

	problems = append(problems, c.Database.validateValues(check, invalid))
	if !invalid["LOCATIONS_BASE_URL"] {
		u, err := url.Parse(c.LocationBaseURL)
		if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
			problems = append(problems, errors.New("LOCATIONS_BASE_URL must have a host and no credentials, query or fragment"))
		} else if port := u.Port(); port != "" {
			if n, err := strconv.ParseUint(port, 10, 16); err != nil || n == 0 {
				problems = append(problems, errors.New("LOCATIONS_BASE_URL port must be between 1 and 65535"))
			}
		}
	}
	if !invalid["HTTP_ADDR"] && !validListenAddress(c.HTTPAddr, check) {
		problems = append(problems, errors.New("HTTP_ADDR must be host:port, with a port between 0 and 65535; bracket IPv6 addresses"))
	}
	return errors.Join(problems...)
}

func validListenAddress(address string, check *validator.Validate) bool {
	host, port, err := net.SplitHostPort(address)
	if err != nil || port == "" {
		return false
	}
	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return false
	}
	if host == "" {
		return true // All interfaces, e.g. :8080 in Docker.
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return true
	}
	return check.Var(host, "hostname_rfc1123") == nil
}
