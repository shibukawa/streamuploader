# syntax=docker/dockerfile:1

FROM golang:1.27-bookworm AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN go tool pw generate
RUN CGO_ENABLED=0 go build -o /out/demo-app ./demo/app

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/demo-app /demo-app
ENV PORT=8081
EXPOSE 8081
ENTRYPOINT ["/demo-app"]
