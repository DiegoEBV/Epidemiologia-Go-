# Imagen multi-etapa: un único build de Go produce los tres binarios y cada
# target final copia solo el suyo (imágenes de ~20 MB sobre alpine).
#
#   docker build --target node -t epidemiologia-go/node .
#   docker build --target api  -t epidemiologia-go/api  .
#   docker build --target pc3  -t epidemiologia-go/pc3  .   (pipeline de la PC3)

FROM node:24-alpine AS frontend
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/index.html ./
COPY web/src ./src
RUN npm run build

FROM golang:1.24-alpine AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOFLAGS=-trimpath
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -ldflags="-s -w" -o /out/node ./cmd/node && \
    go build -ldflags="-s -w" -o /out/api  ./cmd/api && \
    go build -ldflags="-s -w" -o /out/pc3  ./cmd/pc3

# ---------- nodo ML ----------
FROM alpine:3.20 AS node
RUN adduser -D -u 10001 app
USER app
COPY --from=build /out/node /usr/local/bin/node
EXPOSE 9000
ENTRYPOINT ["/usr/local/bin/node"]

# ---------- API coordinadora ----------
FROM alpine:3.20 AS api
RUN adduser -D -u 10001 app
USER app
COPY --from=build /out/api /usr/local/bin/api
COPY --from=frontend /web/dist /app/web
ENV WEB_DIR=/app/web
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --start-period=20s CMD wget -qO- http://localhost:8080/health >/dev/null || exit 1
ENTRYPOINT ["/usr/local/bin/api"]

# ---------- pipeline PC3 (se mantiene) ----------
FROM alpine:3.20 AS pc3
WORKDIR /app
COPY --from=build /out/pc3 /app/pc3
VOLUME ["/app/data", "/app/results"]
ENTRYPOINT ["/app/pc3", "-positivos", "/app/data/positivos_covid.csv", "-fallecidos", "/app/data/fallecidos_covid.csv", "-out", "/app/results"]
