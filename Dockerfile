FROM golang:1.15
RUN git clone https://github.com/chr-fritz/fritzbox_exporter.git /go/src/github.com/chr-fritz/fritzbox_exporter
WORKDIR /go/src/github.com/chr-fritz/fritzbox_exporter
RUN go mod download && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -ldflags '-w -extldflags "-static"' -o fritzbox-exporter .

FROM alpine:latest
RUN apk --no-cache add ca-certificates
COPY --from=0 /go/src/github.com/chr-fritz/fritzbox_exporter/fritzbox-exporter /fritzbox-exporter/
COPY --from=0 /go/src/github.com/chr-fritz/fritzbox_exporter/*.json /etc/fritzbox-exporter/
ENTRYPOINT ["/fritzbox-exporter/fritzbox-exporter"]
CMD ["--metrics-file","/etc/fritzbox-exporter/metrics.json","--listen-address","0.0.0.0:8080"]
