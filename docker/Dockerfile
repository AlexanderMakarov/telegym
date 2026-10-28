FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download || true
COPY . .
# Cross-compile on the build host instead of emulating the target platform.
ARG TARGETOS TARGETARCH
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=$VERSION" \
    -o /out/telegym-mock ./cmd/telegym-mock

FROM alpine:3.20
RUN apk add --no-cache ca-certificates \
    && addgroup -g 10001 -S telegym \
    && adduser  -u 10001 -S -G telegym telegym
COPY --from=build /out/telegym-mock /usr/local/bin/telegym-mock
USER 10001:10001
EXPOSE 5678 9104
ENTRYPOINT ["/usr/local/bin/telegym-mock"]
