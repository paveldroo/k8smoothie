FROM golang:1.24

WORKDIR /app/
COPY main.go /app/

RUN go build -o /app/gracefulapp /app/main.go

ENTRYPOINT [ "/app/gracefulapp" ]
