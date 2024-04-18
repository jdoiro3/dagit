FROM scratch
ENTRYPOINT ["/dagit"]
COPY dagit /

EXPOSE 8080