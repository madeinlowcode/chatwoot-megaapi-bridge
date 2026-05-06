# syntax=docker/dockerfile:1
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ENV CGO_ENABLED=0
RUN go build -trimpath -ldflags="-s -w" -o /out/bridge ./cmd/bridge

FROM scratch
COPY --from=build /out/bridge /bridge
EXPOSE 8080
ENTRYPOINT ["/bridge"]
CMD ["serve"]
