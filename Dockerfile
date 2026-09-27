# ----------------------------
# 1) Build stage
# ----------------------------
FROM golang:1.24-alpine AS builder

RUN apk add --no-cache build-base git

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=1 go build -o pocketbase .



# ----------------------------
# 2) Final runtime stage
# ----------------------------
FROM alpine:latest

RUN apk add --no-cache \
    ca-certificates \
    python3 \
    py3-pip \
    ffmpeg \
    openssh \
    unzip

RUN pip install yt-dlp --break-system-packages

# COPY migrations /pb/migrations
COPY pb_hooks /pb/pb_hooks

COPY --from=builder /app/pocketbase /pb/pocketbase

EXPOSE 8080

CMD ["/pb/pocketbase", "serve", "--http=0.0.0.0:8080"]
