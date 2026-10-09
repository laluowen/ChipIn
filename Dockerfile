# Built from GoReleaser's prebuilt binaries (see .goreleaser.yaml's `builds`
# and `dockers_v2` sections) — this Dockerfile does NOT run `go build`, since
# that would compile the binary a second time, once per target platform, on
# top of GoReleaser already having done it. For a plain `docker build .`
# without GoReleaser (e.g. quick local testing), build the binary yourself
# first: CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o linux/amd64/chipin
# ./cmd/chipin, then `docker build --build-context .=.` from here — or just
# run `goreleaser release --snapshot --clean`, which does both steps for
# every supported platform.
FROM gcr.io/distroless/static-debian13:nonroot@sha256:2293b36c7c9082bf4115aab724b4d2cddec82c8eba39bf27ac0517e159acf150
ARG TARGETPLATFORM
WORKDIR /app
COPY $TARGETPLATFORM/chipin /app/chipin
EXPOSE 8080
USER nonroot:nonroot
CMD ["/app/chipin"]
