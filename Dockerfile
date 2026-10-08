FROM docker.io/library/golang:1.27.1@sha256:1e93e00a31255c07e9a34c4207f3006e1501730c5323697cee7dfb827fdae44c AS builder
ARG VERSION=dev
WORKDIR /app
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=${VERSION}" -o /app/bin/chipin ./cmd/chipin

FROM gcr.io/distroless/static-debian13:nonroot@sha256:2293b36c7c9082bf4115aab724b4d2cddec82c8eba39bf27ac0517e159acf150
WORKDIR /app
COPY --from=builder /app/bin/chipin /app/chipin
EXPOSE 8080
USER nonroot:nonroot
CMD ["/app/chipin"]
