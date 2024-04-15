ARG GO_VERSION=1.18
ARG NODE_VERSION=14.16.1
ARG ALPINE_VERSION=3.13.5

FROM node:${NODE_VERSION}-alpine AS node-builder
WORKDIR /app
COPY nextjs/package.json nextjs/yarn.lock ./
RUN yarn install --frozen-lockfile
COPY nextjs/ .
ENV NEXT_TELEMETRY_DISABLED=1
RUN yarn run export

FROM golang:${GO_VERSION}-alpine AS go-builder
WORKDIR /app
COPY go.mod dagit.go server.go ./
COPY --from=node-builder /app/dist ./nextjs/dist
RUN go install .

FROM alpine:${ALPINE_VERSION}
WORKDIR /app
COPY --from=go-builder /app/golang-nextjs-portable .

ENTRYPOINT ["dagit"]

EXPOSE 8080