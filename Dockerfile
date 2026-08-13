# syntax=docker/dockerfile:1

FROM golang:1.26.5-alpine3.24 AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY *.go ./

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/recall .

FROM alpine:3.24

# A named volume mounted at /tmp/recall inherits this owner and mode.
RUN adduser -D -H -u 10001 recall \
 && install -d -o recall -g recall -m 700 /tmp/recall
USER recall

COPY --from=build /out/recall /usr/local/bin/recall

ENTRYPOINT ["recall"]
