# Build stage
FROM golang:1.25-bookworm AS build

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /app/server ./cmd/server

# Final stage — static binary, no JDK/JRE, minimal base image.
FROM gcr.io/distroless/static-debian12

WORKDIR /app
COPY --from=build /app/server /app/server

EXPOSE 8083

ENTRYPOINT ["/app/server"]
