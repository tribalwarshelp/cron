FROM golang:alpine

ENV MODE=production

WORKDIR /go/src/app
COPY . .

RUN go build -o main .

ADD https://github.com/ufoscout/docker-compose-wait/releases/download/2.2.1/wait ./wait
RUN chmod +x ./wait

CMD ./wait && ./main