# syntax=docker/dockerfile:1

FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/demo-app ./demo/app

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/demo-app /demo-app
EXPOSE 8081
ENTRYPOINT ["/demo-app"]
