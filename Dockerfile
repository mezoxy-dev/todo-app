# Todo App: the built frontend and the Go server in one small image.
# Run it with compose.yaml: docker compose up -d

FROM node:24.18.0-alpine AS web
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

FROM golang:1.26.1-alpine AS server
WORKDIR /src/backend
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server && mkdir /out/data

# No shell, no package manager; runs as the non-root user 65532.
FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=server /out/server /app/server
COPY --from=web /src/frontend/dist /app/web
COPY --from=server --chown=65532:65532 /out/data /data
ENV ADDR=0.0.0.0:8080 DB_PATH=/data/todos.db STATIC_DIR=/app/web
EXPOSE 8080
USER 65532:65532
ENTRYPOINT ["/app/server"]
