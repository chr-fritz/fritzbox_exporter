FROM golang:alpine as build

WORKDIR /usr/src/fritzbox_exporter
COPY . .

RUN go mod download; \
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -ldflags '-w -extldflags "-static"' -o fritzbox-exporter .

FROM alpine:latest
RUN apk --no-cache add ca-certificates
COPY --from=build /usr/src/fritzbox_exporter/fritzbox-exporter /bin/
COPY --from=build /usr/src/fritzbox_exporter/*.json /etc/fritzbox-exporter/
ENTRYPOINT ["/bin/fritzbox-exporter"]
CMD ["--metrics-file","/etc/fritzbox-exporter/metrics.json","--lua-metrics-file","/etc/fritzbox-exporter/metrics-lua.json","--listen-address","0.0.0.0:8080"]
