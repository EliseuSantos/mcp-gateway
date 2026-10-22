# syntax=docker/dockerfile:1

FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/echo-server ./examples/echo-server

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/echo-server /echo-server
USER nonroot:nonroot
EXPOSE 8081
ENTRYPOINT ["/echo-server", "-addr", ":8081"]
