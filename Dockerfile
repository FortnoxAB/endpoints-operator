FROM alpine:3.10
RUN apk add --no-cache ca-certificates
COPY endpoints-operator /
ENTRYPOINT ["/endpoints-operator"]
