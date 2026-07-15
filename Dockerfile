FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=docker
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o /cando1 .

FROM gcr.io/distroless/static-debian12
COPY --from=build /cando1 /cando1
ENTRYPOINT ["/cando1"]
# Provide a config via a bind mount, e.g.:
#   docker run -v $PWD/cfg.toml:/cfg.toml -p 443:443 cando1 -c /cfg.toml
