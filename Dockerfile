# syntax=docker/dockerfile:1.7

# ---- build stage --------------------------------------------------------
FROM golang:1.25-alpine AS build

WORKDIR /src

# Cache module downloads in a separate layer.
COPY go.mod go.sum ./
RUN go mod download

# Copy source. Templates, CSS, and the Simpany .xlsx template are all
# embedded into the binary via go:embed — runtime stage needs nothing else.
COPY . .

ENV CGO_ENABLED=0 GOOS=linux GOFLAGS=-trimpath

ARG VERSION=dev
RUN go build -ldflags="-s -w -X main.version=${VERSION}" -o /out/reviz-accounting .

# ---- runtime stage ------------------------------------------------------
# distroless/static — no shell, no libc; ~2 MB. modernc.org/sqlite is pure Go
# so we do not need glibc/musl.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/reviz-accounting /app/reviz-accounting

# Cloud Run injects PORT (default 8080); our binary already honours $PORT.
ENV PORT=8080
EXPOSE 8080

ENTRYPOINT ["/app/reviz-accounting"]
