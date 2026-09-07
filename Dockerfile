# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.26.5-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w -X main.version=$VERSION" -o /out/tg-notification-relay .

FROM alpine:3.23
RUN apk add --no-cache ca-certificates && addgroup -g 10001 relay && adduser -D -H -u 10001 -G relay relay && mkdir /data && chown relay:relay /data
COPY --from=build /out/tg-notification-relay /usr/local/bin/tg-notification-relay
COPY LICENSE /usr/share/licenses/tg-notification-relay/LICENSE
USER 10001:10001
WORKDIR /data
ENV TG_SESSION=/data/telegram.session.json STATE_FILE=/data/state.json
ENTRYPOINT ["tg-notification-relay"]
CMD ["run"]
