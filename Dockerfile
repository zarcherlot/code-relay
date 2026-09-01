FROM golang:1.27-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
COPY cmd ./cmd
COPY internal ./internal
COPY schemas ./schemas
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/code-relay-mcp ./cmd/code-relay-mcp

FROM alpine:3.22

RUN apk add --no-cache ca-certificates git \
    && addgroup -S code-relay \
    && adduser -S -G code-relay code-relay
COPY --from=build /out/code-relay-mcp /usr/local/bin/code-relay-mcp
RUN mkdir -p /workspace && chown -R code-relay:code-relay /workspace

USER code-relay
WORKDIR /workspace
ENV PORT=8080
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/code-relay-mcp"]
