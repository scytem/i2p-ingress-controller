FROM golang AS build-stage
ADD . /go/src/github.com/scytem/i2p-ingress-controller
RUN cd /go/src/github.com/scytem/i2p-ingress-controller && \
    CGO_ENABLED=0 GOOS=linux go build -a -ldflags '-extldflags "-static"' -o i2p-ingress-controller

FROM phusion/baseimage
RUN add-apt-repository ppa:purplei2p/i2pd && \
    apt-get update && \
    apt-get install -y i2pd && \
    rm /var/lib/i2pd/tunnels.conf
WORKDIR /i2p-controller
COPY --from=build-stage /go/src/github.com/scytem/i2p-ingress-controller /i2p-controller/
ENTRYPOINT ["./i2p-ingress-controller"] 