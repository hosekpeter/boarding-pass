-- +goose Up
CREATE TABLE IF NOT EXISTS boarding_passes (
    sequence BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    id UUID NOT NULL UNIQUE,
    parsed_at TIMESTAMPTZ NOT NULL,
    passenger_name TEXT NOT NULL,
    decoded_bcbp JSONB NOT NULL
);

CREATE INDEX IF NOT EXISTS boarding_passes_newest
    ON boarding_passes (parsed_at DESC, sequence DESC);

-- +goose Down
DROP TABLE IF EXISTS boarding_passes;
