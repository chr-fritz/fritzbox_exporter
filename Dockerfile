FROM --platform=$BUILDPLATFORM golang:1.15 AS build

ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT

RUN git clone https://github.com/chr-fritz/fritzbox_exporter.git /go/src/github.com/chr-fritz/fritzbox_exporter
WORKDIR /go/src/github.com/chr-fritz/fritzbox_exporter

RUN go mod download; \
	case "$TARGETVARIANT" in \
		v5) export GOARM=5 ;; \
		v6) export GOARM=6 ;; \
		v7) export GOARM=7 ;; \
	esac; \
	CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -a -ldflags '-w -extldflags "-static"' -o fritzbox-exporter .
 
FROM --platform=$TARGETPLATFORM alpine:latest
RUN apk --no-cache add ca-certificates
COPY --from=build /go/src/github.com/chr-fritz/fritzbox_exporter/fritzbox-exporter /fritzbox-exporter/
COPY --from=build /go/src/github.com/chr-fritz/fritzbox_exporter/*.json /etc/fritzbox-exporter/
ENTRYPOINT ["/fritzbox-exporter/fritzbox-exporter"]
CMD ["--metrics-file","/etc/fritzbox-exporter/metrics.json","--listen-address","0.0.0.0:8080"]
