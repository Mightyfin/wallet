# syntax=docker/dockerfile:1

FROM golang:1.25-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux go test ./... \
    && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/wallet-ledger-api ./cmd/api \
    && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/wallet-ledger-migrate ./cmd/migrate \
    && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/wallet-ledger-outbox-publisher ./cmd/outbox-publisher

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/wallet-ledger-api /wallet-ledger-api
COPY --from=build /out/wallet-ledger-migrate /wallet-ledger-migrate
COPY --from=build /out/wallet-ledger-outbox-publisher /wallet-ledger-outbox-publisher
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/wallet-ledger-api"]
