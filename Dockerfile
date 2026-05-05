FROM golang:1.23-alpine AS build
WORKDIR /src
RUN apk add --no-cache git ca-certificates
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/bridge ./cmd/bridge-api && \
    CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/bridge-worker ./cmd/bridge-worker

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /out/bridge /bridge
COPY --from=build /out/bridge-worker /bridge-worker
USER 65534:65534
EXPOSE 8080
ENTRYPOINT ["/bridge"]
CMD ["serve"]
