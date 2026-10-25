# The GophKeeper server. The client runs on the user's machine and is built
# with `make dist` instead.
FROM golang:1.26-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG BUILD_VERSION=N/A
ARG BUILD_COMMIT=N/A
RUN CGO_ENABLED=0 go build \
    -ldflags "-s -w -X main.buildVersion=${BUILD_VERSION} -X main.buildCommit=${BUILD_COMMIT} -X main.buildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    -o /out/gophkeeper-server ./cmd/gophkeeper-server

FROM alpine:3.22

RUN adduser -D -H gophkeeper
USER gophkeeper

COPY --from=build /out/gophkeeper-server /usr/local/bin/gophkeeper-server

# gRPC, then the REST gateway with Swagger.
EXPOSE 3200 3201
ENTRYPOINT ["gophkeeper-server"]
