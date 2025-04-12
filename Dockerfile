ARG GOLANG_VERSION=1.24
ARG ALPINE_VERSION=3.21

FROM golang:${GOLANG_VERSION}-alpine${ALPINE_VERSION} AS build

ADD ./publy /app

WORKDIR /app

RUN go build


FROM alpine:${ALPINE_VERSION} AS prod

ENV PUBLY_HOST=127.0.0.1 \
    PUBLY_PORT=8000

ADD ./entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

COPY --from=build /app/publy.io /app/
