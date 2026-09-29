package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/jackc/pgx/v5"
)

// DBConfig describes the PostgreSQL connection and its pool bounds.
type DBConfig struct {
	Host     string `env:"DB_HOST" envDefault:"127.0.0.1" validate:"required"`
	Port     int    `env:"DB_PORT" envDefault:"5432" validate:"gte=1,lte=65535"`
	User     string `env:"DB_USER" envDefault:"boarding_pass" validate:"required"`
	Password string `env:"DB_PASSWORD" envDefault:"boarding_pass"`
	Name     string `env:"DB_NAME" envDefault:"boarding_pass" validate:"required"`
	SSLMode  string `env:"DB_SSLMODE" envDefault:"disable" validate:"oneof=disable allow prefer require verify-ca verify-full"`
	Schema   string `env:"DB_SCHEMA" envDefault:"public" validate:"required"`

	MaxConns         int32         `env:"DB_MAX_CONNS" envDefault:"20" validate:"gte=1,gtefield=MinConns"`
	MinConns         int32         `env:"DB_MIN_CONNS" envDefault:"2" validate:"gte=0"`
	StatementTimeout time.Duration `env:"DB_STATEMENT_TIMEOUT" envDefault:"5s" validate:"gte=1000000"`
	ConnectTimeout   time.Duration `env:"DB_CONNECT_TIMEOUT" envDefault:"30s" validate:"gt=0"`
}

// ConnectionString safely encodes credentials, IPv6 hosts and database names.
func (c DBConfig) ConnectionString() string {
	query := url.Values{
		"sslmode":           {c.SSLMode},
		"statement_timeout": {strconv.FormatInt(c.StatementTimeout.Milliseconds(), 10)},
	}
	if c.Schema != "" {
		query.Set("search_path", pgx.Identifier{c.Schema}.Sanitize())
	}
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(c.User, c.Password),
		Host:     net.JoinHostPort(c.Host, strconv.Itoa(c.Port)),
		Path:     "/" + c.Name,
		RawPath:  "/" + url.PathEscape(c.Name),
		RawQuery: strings.ReplaceAll(query.Encode(), "+", "%20"),
	}
	return u.String()
}

func (c DBConfig) validateValues(check *validator.Validate, invalid map[string]bool) error {
	var problems []error
	if !invalid["DB_HOST"] && !invalid["DB_PORT"] && !validListenAddress(net.JoinHostPort(c.Host, strconv.Itoa(c.Port)), check) {
		problems = append(problems, errors.New("DB_HOST must be a hostname or IP address"))
	}
	for _, field := range []struct{ name, value string }{
		{"DB_USER", c.User}, {"DB_PASSWORD", c.Password}, {"DB_NAME", c.Name}, {"DB_SCHEMA", c.Schema},
	} {
		if strings.ContainsRune(field.value, '\x00') {
			problems = append(problems, fmt.Errorf("%s must not contain NUL characters", field.name))
		}
	}
	return errors.Join(problems...)
}
