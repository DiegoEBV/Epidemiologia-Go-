# Imagen del pipeline PC3 (en la PC4 se separa en coordinador y nodos ML).
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 go build -o /out/pc3 ./cmd/pc3

FROM alpine:3.20
WORKDIR /app
COPY --from=build /out/pc3 /app/pc3
VOLUME ["/app/data", "/app/results"]
ENTRYPOINT ["/app/pc3", "-positivos", "/app/data/positivos_covid.csv", "-fallecidos", "/app/data/fallecidos_covid.csv", "-out", "/app/results"]
