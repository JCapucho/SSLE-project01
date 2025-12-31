FROM golang:1.25 as build

WORKDIR /go/src/app

COPY cmd/go.mod cmd/go.sum /go/src/app
COPY services/go.mod services/go.sum /go/src/services

RUN go mod download

COPY cmd /go/src/app
COPY services /go/src/services

RUN go vet -v
RUN go test -v

RUN CGO_ENABLED=0 go build -o /go/bin/app

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /go/bin/app /
CMD ["/app"]
