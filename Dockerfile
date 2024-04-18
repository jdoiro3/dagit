FROM alpine

RUN apk add --no-cache bash
RUN apk --update add jq
ENTRYPOINT ["/dagit"]
COPY dagit /bin/dagit
EXPOSE 8080