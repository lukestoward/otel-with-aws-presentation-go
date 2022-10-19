FROM golang:1.19.2

WORKDIR /app

COPY go.mod ./
COPY go.sum ./
RUN go mod download && go mod verify

COPY . ./

RUN go build -o service-b

EXPOSE 8001

CMD ["./service-b"]