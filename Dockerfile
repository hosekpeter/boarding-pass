FROM golang:1.27.1-bookworm AS base
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

FROM base AS test
RUN apt-get update \
    && apt-get install -y --no-install-recommends make poppler-utils \
    && rm -rf /var/lib/apt/lists/*
CMD ["make", "check"]

FROM base AS build
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /boarding-pass .

FROM debian:bookworm-slim AS runtime
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates poppler-utils \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 10001 app \
    && useradd --uid 10001 --gid app --no-create-home app
COPY --from=build /boarding-pass /usr/local/bin/boarding-pass
USER app
ENV HTTP_ADDR=:8080
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/boarding-pass"]
