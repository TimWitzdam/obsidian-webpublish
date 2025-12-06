FROM golang:1.25.5-alpine AS builder

ARG LDFLAGS="-s -w"

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 go build -ldflags="${LDFLAGS}" -o main main.go


# ----------------------------------------------------------------------


FROM scratch

COPY --from=builder /app/main /main

ENV GIN_MODE=release

CMD ["/main"]