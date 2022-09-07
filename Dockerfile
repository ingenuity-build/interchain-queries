FROM golang:1.18-bullseye as build

RUN apt update && apt install git
WORKDIR /src/app
ENV GIT_SSH_COMMAND="ssh -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no"
RUN git config --global url."git@github.com:".insteadOf "https://github.com/"

COPY go.mod go.mod
COPY go.sum go.sum

RUN go mod download

COPY . .

RUN go build

FROM debian:bullseye

COPY --from=build /src/app/interchain-queries /usr/local/bin/interchain-queries

RUN adduser --system --home /icq --disabled-password --disabled-login icq -U 1000

USER icq
