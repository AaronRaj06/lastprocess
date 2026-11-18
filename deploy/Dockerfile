# Build: docker build -f deploy/Dockerfile -t lastprocess .
# Cross-compiles on the CI runner's own architecture, so a multi-arch build needs no emulation.
FROM --platform=$BUILDPLATFORM golang:1.27 AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# No cgo anywhere (pure-Go SQLite, Lua VM and ONNX runner), so the binary is static.
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /server ./cmd/server && mkdir /data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /server /server
# A fresh volume copies this directory's ownership, so the non-root user can write to it.
COPY --from=build --chown=65532:65532 /data /data
# /data holds the SQLite database and the SSH host key; mount a volume here so
# both survive redeploys (a changed host key makes every client see a warning).
VOLUME /data
EXPOSE 2222
ENTRYPOINT ["/server", "-addr", ":2222", "-data", "/data", "-fill", "rl,baseline"]
