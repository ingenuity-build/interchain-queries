FROM golang:1.18-bullseye

RUN apt update && apt install git
WORKDIR /src/app
COPY go.mod go.mod
COPY go.sum go.sum

RUN go mod download

COPY . .

RUN go build

RUN ln -s /src/app/interchain-queries /usr/local/bin
RUN adduser --system --home /icq --disabled-password --disabled-login icq -U 1000
USER icq
