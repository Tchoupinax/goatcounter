FROM golang:latest AS builder

WORKDIR /app

COPY . .

RUN go build \
	-ldflags="-X zgo.at/goatcounter/v2.Version=$(git log -n1 --format='%h_%cI')" \
	-o goatcounter \
	./cmd/goatcounter

###

FROM ubuntu

WORKDIR /app

COPY --from=builder /app/goatcounter /app/goatcounter

RUN chmod +x goatcounter

CMD ./goatcounter serve -listen=0.0.0.0:80 -tls=none
